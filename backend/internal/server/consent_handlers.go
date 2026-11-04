package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/consent"
	"hatamoto.dev/core/internal/render"
)

// consentBodyMax caps the counter beacon body: {"choice":"custom"} is 19 bytes.
const consentBodyMax = 256

// installConsentRender gives the page engine its cookie consent settings. The
// hook fails closed for the visitor: when the store cannot be read and nothing
// is cached, it serves the default categories (all denied until chosen) rather
// than letting the script 404 and every category read as granted.
func (s *Server) installConsentRender() {
	render.SetConsent(func(ctx context.Context) (render.ConsentConfig, bool) {
		if s.consentStore == nil {
			return render.ConsentConfig{}, false
		}
		set, err := s.consentStore.Get(ctx)
		if err != nil {
			s.logger.Warn("consent settings unreadable, serving the default banner", "error", err)
			set = consent.Defaults()
			set.Enabled = true
		}
		if !set.Enabled {
			return render.ConsentConfig{}, false
		}
		strs := make(map[string]string, len(consent.DefaultStrings))
		for k, v := range consent.DefaultStrings {
			strs[k] = v
		}
		return render.ConsentConfig{
			Version:    set.Version(),
			Categories: set.Categories,
			Region:     set.Region,
			Title:      set.Title,
			Text:       set.Text,
			Strings:    strs,
		}, true
	})
}

// consentCountry is the visitor's country from the CDN header, "" when absent.
func consentCountry(r *http.Request) string {
	if c := r.Header.Get("CF-IPCountry"); c != "" {
		return c
	}
	return r.Header.Get("CloudFront-Viewer-Country")
}

// handleConsentRegion tells the browser whether to show the banner. The page
// HTML is the same for every country; only this answer depends on the CDN
// header, so it is never cached. A missing header or store shows the banner.
func (s *Server) handleConsentRegion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Add("Vary", "CF-IPCountry, CloudFront-Viewer-Country")
	show := true
	if s.consentStore != nil {
		if set, err := s.consentStore.Get(r.Context()); err == nil {
			show = consent.RegionShow(set.Region, consentCountry(r))
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"show": show})
}

// handleConsentLog counts one visitor choice for the day. Nothing about the
// visitor is read beyond the user agent used to drop bots, and none of it is stored.
func (s *Server) handleConsentLog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Choice string `json:"choice"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, consentBodyMax)).Decode(&body); err != nil || !consent.ValidChoice(body.Choice) {
		http.Error(w, `{"error":"invalid_payload"}`, http.StatusBadRequest)
		return
	}
	if ua := r.UserAgent(); ua == "" || agents.Default().LooksLikeBot(ua) || s.consentCounter == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.consentCounter.Inc(r.Context(), time.Now(), body.Choice); err != nil {
		s.logger.Warn("consent count failed", "error", err)
		http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// privacyAdmin adapts the consent store and counter to admin.PrivacyService.
type privacyAdmin struct {
	store   *consent.Store
	counter *consent.Counter
}

func (p privacyAdmin) Get(ctx context.Context) (consent.Settings, error) { return p.store.Get(ctx) }
func (p privacyAdmin) Save(ctx context.Context, s consent.Settings) error {
	return p.store.Save(ctx, s)
}
func (p privacyAdmin) Counts(ctx context.Context, days int) ([]consent.DayCounts, error) {
	return p.counter.Last(ctx, days)
}
