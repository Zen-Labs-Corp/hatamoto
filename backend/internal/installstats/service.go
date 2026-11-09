package installstats

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/release"
	"hatamoto.dev/core/internal/safefetch"
)

// ErrForcedOff means HATAMOTO_TELEMETRY=off is set, so the owner cannot
// change the choice.
var ErrForcedOff = errors.New("install stats are turned off by the server configuration")

// userAgent names the sender. It carries no version or install detail.
const userAgent = "hatamoto-stats"

// Backend is the stored state the Service works on; *Store implements it.
type Backend interface {
	Load(ctx context.Context) (State, error)
	Enable(ctx context.Context, level Level) (State, error)
	Disable(ctx context.Context) error
	Reset(ctx context.Context) (State, error)
	Claim(ctx context.Context, version string) (id string, ok bool, err error)
	AddHealth(ctx context.Context, d Counts) error
	RecordResult(ctx context.Context, status, version string, sent Counts) error
	DismissNudge(ctx context.Context) error
	Banner(ctx context.Context) (BannerKind, error)
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
	Health  *Health                      // in-memory counts to flush; nil means none
	Sources Sources                      // facts for the Detailed body; nil sends Basic instead
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
	health  *Health
	sources Sources
	warned  atomic.Bool // the "no sources" notice was logged
}

// New returns a Service. An invalid HATAMOTO_TELEMETRY_URL is reported once
// and the default endpoint is used.
func New(c Config) *Service {
	s := &Service{store: c.Store, version: c.Version, channel: c.Channel, getenv: c.Getenv, http: c.HTTP, log: c.Log, health: c.Health, sources: c.Sources}
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
	Level     Level // off under HATAMOTO_TELEMETRY=off
	ForcedOff bool
	LastSent  *time.Time // last successful report
}

// View loads the stored choice.
func (s *Service) View(ctx context.Context) (View, error) {
	st, err := s.store.Load(ctx)
	if err != nil {
		return View{}, err
	}
	if ForcedOff(s.getenv) {
		return View{Level: LevelOff, ForcedOff: true}, nil
	}
	return View{Level: st.Level, LastSent: st.LastOKAt}, nil
}

// Health returns the in-memory collector the Service flushes; nil when none
// was configured. Every Health method is safe on nil.
func (s *Service) Health() *Health { return s.health }

// ForcedOff reports whether HATAMOTO_TELEMETRY=off is set.
func (s *Service) ForcedOff() bool { return ForcedOff(s.getenv) }

// Banner says which owner banner to show. It reads the choice only, so it is
// cheap enough for every page view. Nothing shows under HATAMOTO_TELEMETRY=off.
func (s *Service) Banner(ctx context.Context) (BannerKind, error) {
	if s.ForcedOff() {
		return BannerNone, nil
	}
	return s.store.Banner(ctx)
}

// DismissNudge hides the Detailed offer for good.
func (s *Service) DismissNudge(ctx context.Context) error {
	if s.ForcedOff() {
		return ErrForcedOff
	}
	return s.store.DismissNudge(ctx)
}

// Choose records the owner's answer. Basic and Detailed create the install ID;
// Off deletes it.
func (s *Service) Choose(ctx context.Context, level Level) error {
	if ForcedOff(s.getenv) {
		return ErrForcedOff
	}
	switch level {
	case LevelOff:
		return s.store.Disable(ctx)
	case LevelBasic, LevelDetailed:
		_, err := s.store.Enable(ctx, level)
		return err
	}
	return fmt.Errorf("install stats: unknown level %q", level)
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
// deleted. Without the variable it only repairs the stored level. Lifting the variable later does not resume sending; the owner
// turns it on again in Settings > Updates.
func (s *Service) Enforce(ctx context.Context) error {
	if !ForcedOff(s.getenv) {
		// Loading repairs a level an older binary left out of step with consent.
		_, err := s.store.Load(ctx)
		return err
	}
	return s.store.Disable(ctx)
}

// Flush moves the in-memory health counts into the database. Counts that
// could not be stored are put back, so nothing is lost to a database error.
func (s *Service) Flush(ctx context.Context) error {
	d := s.health.Drain()
	if d.IsZero() {
		return nil
	}
	if err := s.store.AddHealth(ctx, d); err != nil {
		s.health.Restore(d)
		return err
	}
	return nil
}

// Run is the hourly job. It makes no request unless the variable is not off,
// the level is Basic or Detailed, the build is a release, and this process won
// the claim. It never retries: the claim stamps the time whatever the outcome.
// A Detailed body that cannot be built (a value the receiver would refuse,
// such as an odd theme version) is replaced by the Basic body, so the install
// still reports and the version counts as reported. Only a Basic body that
// cannot be encoded ends as "error: build". Neither the ID nor the URL is
// logged or stored in the status.
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
	if err := s.Flush(ctx); err != nil {
		s.log.Warn("install stats: could not store health counts", "error", err)
	}
	id, ok, err := s.store.Claim(ctx, s.version)
	if err != nil || !ok {
		return err
	}
	st, err := s.store.Load(ctx)
	if err != nil {
		return err
	}
	body, sent, err := s.body(ctx, id, st)
	if err != nil {
		s.log.Warn("install stats: body not built, nothing sent", "error", err)
		return s.store.RecordResult(ctx, "error: build", s.version, Counts{})
	}
	status := s.post(ctx, body)
	s.log.Info("install stats sent", "status", status)
	return s.store.RecordResult(ctx, status, s.version, sent)
}

// body builds the request body for the stored level, and returns the health
// counts it carries. Detailed falls back to Basic without sources, or when
// the Detailed body cannot be built; the counts then stay stored.
func (s *Service) body(ctx context.Context, id string, st State) ([]byte, Counts, error) {
	if st.Level == LevelDetailed {
		if s.sources != nil {
			now := time.Now()
			b, err := BuildDetailed(ctx, BuildInput{
				Install: id, Version: s.version, Channel: s.channelName(ctx),
				Src: s.sources, Counts: st.Counts, WindowStart: st.WindowStart, Now: now,
			})
			if err == nil {
				return b, st.Counts, nil
			}
			s.log.Warn("install stats: detailed body not built, sending the basic body", "error", err)
		} else if s.warned.CompareAndSwap(false, true) {
			s.log.Warn("install stats: no detailed sources, sending the basic body")
		}
	}
	b, err := encodeBasic(Payload{Install: id, Version: s.version, Channel: s.channelName(ctx)})
	return b, Counts{}, err
}

func (s *Service) channelName(ctx context.Context) string {
	if s.channel == nil {
		return ""
	}
	return s.channel(ctx)
}

// encodeBasic is the Basic body: exactly the three keys of Payload.
func encodeBasic(p Payload) ([]byte, error) { return jsonx.MarshalPinned(p) }

// post sends body and returns the status text to store.
func (s *Service) post(ctx context.Context, body []byte) string {
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
