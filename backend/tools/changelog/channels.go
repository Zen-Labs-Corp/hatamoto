package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// channelOrder is the promotion order: a release shows the furthest channel it
// has reached.
var channelOrder = []string{"canary", "edge", "stable"}

// Live is where a release stands in the published channel manifests.
type Live struct {
	Channel string // furthest channel whose first ramp step has started; "" if none yet
	Yanked  bool   // listed as yanked in any channel
}

// manifestFile is a channel file as served: the signed index, optionally
// wrapped as {"index": ..., "sig": ...}.
type manifestFile struct {
	Index    *manifestIndex `json:"index"`
	Releases []manifestRel  `json:"releases"`
	Yanked   []struct {
		Version string `json:"version"`
	} `json:"yanked"`
}

type manifestIndex struct {
	Releases []manifestRel `json:"releases"`
	Yanked   []struct {
		Version string `json:"version"`
	} `json:"yanked"`
}

type manifestRel struct {
	Version string `json:"version"`
	Halted  bool   `json:"halted"`
	Ramp    []struct {
		From string `json:"from"`
	} `json:"ramp"`
}

// LiveChannels reads canary, edge and stable from base (the /releases URL) and
// says, per version, the furthest channel it is live on at now and whether it
// is yanked. A channel counts once its first ramp step has started, so a
// release scheduled for stable in a week is still edge (or canary) today, and
// only while it is not halted there (installs are not offered a halted entry).
// It is an error when no channel manifest exists at all (a wrong base URL
// must not look like "nothing released yet").
// The manifests are public: no key is sent.
func LiveChannels(hc *http.Client, base string, now time.Time) (map[string]Live, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	out := map[string]Live{}
	found := 0
	for rank, ch := range channelOrder {
		m, ok, err := fetchManifest(hc, strings.TrimRight(base, "/")+"/channels/"+ch+".json")
		if err != nil {
			return nil, fmt.Errorf("%s manifest: %w", ch, err)
		}
		if ok {
			found++
		}
		for _, r := range m.Releases {
			if len(r.Ramp) == 0 || r.Halted {
				continue
			}
			from, err := time.Parse(time.RFC3339, r.Ramp[0].From)
			if err != nil {
				return nil, fmt.Errorf("%s manifest: %s ramp start %q: %w", ch, r.Version, r.Ramp[0].From, err)
			}
			if from.After(now) {
				continue
			}
			l := out[r.Version]
			if l.Channel == "" || rankOf(l.Channel) < rank {
				l.Channel = ch
			}
			out[r.Version] = l
		}
		for _, y := range m.Yanked {
			l := out[y.Version]
			l.Yanked = true
			out[y.Version] = l
		}
	}
	if found == 0 {
		return nil, fmt.Errorf("no channel manifest at %s/channels/ (check HATAMOTO_URL or HATAMOTO_RELEASES_URL)", strings.TrimRight(base, "/"))
	}
	return out, nil
}

func rankOf(ch string) int {
	for i, c := range channelOrder {
		if c == ch {
			return i
		}
	}
	return -1
}

// fetchManifest reports ok=false for a 404 (a channel nobody has published to).
func fetchManifest(hc *http.Client, u string) (manifestIndex, bool, error) {
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := cp.Get(u)
	if err != nil {
		return manifestIndex{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return manifestIndex{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return manifestIndex{}, false, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return manifestIndex{}, false, err
	}
	var f manifestFile
	if err := json.Unmarshal(b, &f); err != nil {
		return manifestIndex{}, false, fmt.Errorf("GET %s: %w", u, err)
	}
	if f.Index != nil {
		return *f.Index, true, nil
	}
	return manifestIndex{Releases: f.Releases, Yanked: f.Yanked}, true, nil
}
