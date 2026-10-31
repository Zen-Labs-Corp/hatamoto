package analytics

import (
	"context"
	"time"
)

// maxKeysPerDim caps the distinct keys one (day, dim) admits for the whole
// day, across flushes; further keys are counted under KeyOther. It only bounds
// memory and rows per day and is set well above trafficTopKeys: the nightly
// compaction (PruneTraffic) decides which keys survive, so a long tail or a
// burst of junk keys early in the day must not crowd out real ones. The
// admitted set lives in memory (at most today and yesterday) and is empty
// after a restart, so a restart can admit up to the cap again.
const maxKeysPerDim = 2000

// TrafficRepository writes buffered traffic rows.
type TrafficRepository interface {
	// AddTraffic adds each row's counters to its (day, dim, k1, k2) row.
	AddTraffic(ctx context.Context, rows []TrafficRow) error
}

type trafficKey struct {
	day    time.Time
	dim    Dim
	k1, k2 string
}

type dayDim struct {
	day time.Time
	dim Dim
}

// SetTrafficRepo sets where Flush writes traffic rows. Without one, traffic
// events are buffered and dropped on Flush.
func (s *Service) SetTrafficRepo(r TrafficRepository) {
	s.mu.Lock()
	s.trafficRepo = r
	s.mu.Unlock()
}

// SetEngagement turns the counting of engagement events on or off.
func (s *Service) SetEngagement(on bool) { s.engagementOff.Store(!on) }

// EngagementEnabled reports whether engagement events are counted (default on).
func (s *Service) EngagementEnabled() bool { return !s.engagementOff.Load() }

// RecordTraffic buffers one classified beacon event: the visit's channel and
// source, plus its campaign, landing page, device and country. It only
// touches memory. A pageview event adds pageviews (and a visit when Entry);
// an engagement event adds engaged (when First), multi or scrolled; a form event adds only its funnel step.
func (s *Service) RecordTraffic(ev Event, channel, source string) {
	var c Counts
	switch ev.Eng {
	case "":
		c.Pageviews = 1
		if ev.Entry {
			c.Visits = 1
		}
	case "multi":
		c.Multi = 1
	case "scroll":
		c.Scrolled = 1
	case "form_seen":
		c.FormSeen = 1
	case "form_start":
		c.FormStarted = 1
	default:
		return
	}
	if (ev.Eng == "multi" || ev.Eng == "scroll") && ev.First {
		c.Engaged = 1
	}
	country := ev.Country
	if country == "" {
		country = CountryUnknown
	}
	day := s.today()

	s.mu.Lock()
	s.bump(day, DimChannel, channel, "", c)
	s.bump(day, DimSource, source, channel, c)
	if ev.UTMCampaign != "" {
		s.bump(day, DimCampaign, ev.UTMCampaign, ev.UTMMedium, c)
	}
	if ev.Path != "" {
		s.bump(day, DimLanding, ev.Path, "", c)
	}
	s.bump(day, DimDevice, ev.Device, "", c)
	s.bump(day, DimCountry, country, "", c)
	s.mu.Unlock()
}

// bump adds c to a row; s.mu must be held.
func (s *Service) bump(day time.Time, dim Dim, k1, k2 string, c Counts) {
	k := trafficKey{day, dim, k1, k2}
	// Channel, device and country have small fixed key sets: never capped.
	if (k1 != KeyOther || k2 != "") && dim != DimChannel && dim != DimDevice && dim != DimCountry {
		dd := dayDim{day, dim}
		admitted := s.trafficKeys[dd]
		if _, ok := admitted[[2]string{k1, k2}]; !ok {
			switch {
			case len(admitted) >= maxKeysPerDim:
				k = trafficKey{day, dim, KeyOther, ""}
			case admitted == nil:
				s.trafficKeys[dd] = map[[2]string]struct{}{{k1, k2}: {}}
			default:
				admitted[[2]string{k1, k2}] = struct{}{}
			}
		}
	}
	cur := s.traffic[k]
	cur.add(c)
	s.traffic[k] = cur
}

// takeTraffic swaps the traffic buffer out and returns its rows.
func (s *Service) takeTraffic() ([]TrafficRow, TrafficRepository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Day rollover: keep today and yesterday (a failed flush may merge back).
	keep := s.today().AddDate(0, 0, -1)
	for dd := range s.trafficKeys {
		if dd.day.Before(keep) {
			delete(s.trafficKeys, dd)
		}
	}
	if len(s.traffic) == 0 {
		return nil, s.trafficRepo
	}
	rows := make([]TrafficRow, 0, len(s.traffic))
	for k, c := range s.traffic {
		rows = append(rows, TrafficRow{Day: k.day, Dim: k.dim, K1: k.k1, K2: k.k2, Counts: c})
	}
	s.traffic = map[trafficKey]Counts{}
	return rows, s.trafficRepo
}

// flushTraffic writes the traffic buffer in one batch. A failed batch is
// merged back so the next flush retries it.
func (s *Service) flushTraffic(ctx context.Context) error {
	rows, repo := s.takeTraffic()
	if len(rows) == 0 || repo == nil {
		return nil
	}
	err := repo.AddTraffic(ctx, rows)
	if err != nil {
		s.mu.Lock()
		for _, r := range rows {
			s.bump(r.Day, r.Dim, r.K1, r.K2, r.Counts)
		}
		s.mu.Unlock()
	}
	return err
}
