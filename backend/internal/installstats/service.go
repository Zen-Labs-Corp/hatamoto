package installstats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/safefetch"
)

// ErrForcedOff means HATAMOTO_TELEMETRY=off is set, so the owner cannot
// change the choice.
var ErrForcedOff = errors.New("install stats are turned off by the server configuration")

// userAgent names the sender. It carries no version or install detail.
const userAgent = "hatamoto-stats"

// placeholderID is shown in the preview before an ID exists.
const placeholderID = "6f1c0a9e5b7d4e28a3c1f09d7b2e4a65"

// Backend is the stored state the Service works on; *Store implements it.
type Backend interface {
	Load(ctx context.Context) (State, error)
	Enable(ctx context.Context) (State, error)
	Disable(ctx context.Context) error
	Reset(ctx context.Context) (State, error)
	Claim(ctx context.Context) (id string, ok bool, err error)
	RecordResult(ctx context.Context, status string) error
}

var _ Backend = (*Store)(nil)

// HTTPDoer is the subset of *http.Client the sender needs.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Config wires a Service.
type Config struct {
	Store   Backend
	Version string                       // running version; a non-release version never sends
	Channel func(context.Context) string // update channel, "" when unknown
	Getenv  func(string) string          // nil means os.Getenv
	HTTP    HTTPDoer                     // nil means a guarded client with a 10 s timeout
	Log     *slog.Logger                 // nil means slog.Default()
}

// Service holds the owner's choice and sends the daily ping.
type Service struct {
	store   Backend
	version string
	channel func(context.Context) string
	getenv  func(string) string
	http    HTTPDoer
	log     *slog.Logger
	url     string
}

// New returns a Service. An invalid HATAMOTO_TELEMETRY_URL is reported once
// and the default endpoint is used.
func New(c Config) *Service {
	s := &Service{store: c.Store, version: c.Version, channel: c.Channel, getenv: c.Getenv, http: c.HTTP, log: c.Log}
	if s.getenv == nil {
		s.getenv = os.Getenv
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.http == nil {
		s.http = newClient(allowLoopbackHTTP())
	}
	var warn string
	s.url, warn = ResolveURL(s.getenv)
	if warn != "" {
		s.log.Warn("install stats: " + warn)
	}
	return s
}

// newClient is the sender's client: short timeout, no cookies, and no
// redirects followed (a redirect response is just an error status).
func newClient(allowPrivate bool) *http.Client {
	c := safefetch.NewClient(safefetch.ClientOptions{Timeout: 10 * time.Second, AllowPrivate: allowPrivate})
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// devVersion matches the versions a source build stamps: a "dev." prerelease
// segment (scripts/version.sh) and a Go pseudo-version (VCS stamping).
var devVersion = regexp.MustCompile(`[.-]dev\.|[.-]\d{14}-[0-9a-f]{12}$`)

// isRelease reports whether v names a published release, not a build from a
// source checkout.
func isRelease(v string) bool { return release.ValidVersion(v) && !devVersion.MatchString(v) }

// View is what the admin shows.
type View struct {
	State       State
	ForcedOff   bool
	Preview     Payload // the exact body; carries a placeholder ID until consent is yes
	PreviewJSON string
}

// View loads the state and builds the preview.
func (s *Service) View(ctx context.Context) (View, error) {
	st, err := s.store.Load(ctx)
	if err != nil {
		return View{}, err
	}
	forced := ForcedOff(s.getenv)
	if forced {
		st.Consent, st.InstallID = ConsentNo, ""
	}
	p := s.payload(ctx, st.InstallID)
	if p.Install == "" {
		p.Install = placeholderID
	}
	return View{State: st, ForcedOff: forced, Preview: p, PreviewJSON: p.Pretty()}, nil
}

// Choose records the owner's answer. Yes creates the install ID; No deletes it.
func (s *Service) Choose(ctx context.Context, yes bool) error {
	if ForcedOff(s.getenv) {
		return ErrForcedOff
	}
	if !yes {
		return s.store.Disable(ctx)
	}
	_, err := s.store.Enable(ctx)
	return err
}

// Reset replaces the install ID with a new one.
func (s *Service) Reset(ctx context.Context) error {
	if ForcedOff(s.getenv) {
		return ErrForcedOff
	}
	_, err := s.store.Reset(ctx)
	return err
}

// Enforce applies HATAMOTO_TELEMETRY=off: consent becomes no and the ID is
// deleted. Lifting the variable later does not resume sending; the owner
// turns it on again in Settings > Updates.
func (s *Service) Enforce(ctx context.Context) error {
	if !ForcedOff(s.getenv) {
		return nil
	}
	return s.store.Disable(ctx)
}

// Run is the daily job. It makes no request unless the variable is not off,
// consent is yes, the build is a release, and this process won the claim for
// the interval. It never retries: the claim stamps the time whatever the
// outcome. Neither the ID nor the URL is logged or stored in the status.
func (s *Service) Run(ctx context.Context) error {
	if err := s.Enforce(ctx); err != nil {
		return err
	}
	if ForcedOff(s.getenv) {
		return nil
	}
	// A development build is not a release; it never reports.
	if !isRelease(s.version) {
		return nil
	}
	id, ok, err := s.store.Claim(ctx)
	if err != nil || !ok {
		return err
	}
	status := s.send(ctx, s.payload(ctx, id))
	s.log.Info("install stats sent", "status", status)
	return s.store.RecordResult(ctx, status)
}

func (s *Service) payload(ctx context.Context, id string) Payload {
	p := Payload{Install: id, Version: s.version}
	if s.channel != nil {
		p.Channel = s.channel(ctx)
	}
	return p
}

// send posts the payload and returns the status text to store.
func (s *Service) send(ctx context.Context, p Payload) string {
	body, _ := json.Marshal(p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return "error: network"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.http.Do(req)
	if err != nil {
		return "error: network"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "ok"
	}
	return "error: http " + strconv.Itoa(resp.StatusCode)
}
