package installstats

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

// memStore mirrors the Store's rules in memory.
type memStore struct {
	mu        sync.Mutex
	st        State
	claimedAt time.Time
	n         int
	disabled  int   // Disable calls
	addErr    error // returned by AddHealth
}

func newMem() *memStore { return &memStore{st: State{Consent: ConsentUnset, Level: LevelUnset}} }

func (m *memStore) Load(context.Context) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.withToday(), nil
}

// withToday fills DetailedSentToday, which the real Store computes in SQL.
func (m *memStore) withToday() State {
	st := m.st
	st.DetailedSentToday = st.DetailedSentAt != nil && st.DetailedSentAt.UTC().Format(time.DateOnly) == time.Now().UTC().Format(time.DateOnly)
	return st
}
func (m *memStore) Enable(_ context.Context, level Level) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Level != level {
		m.st.Counts, m.st.Sent, m.st.WindowStart, m.st.DetailedSentAt = Counts{}, Counts{}, nil, nil
		if level == LevelDetailed {
			now := time.Now()
			m.st.WindowStart = &now
		}
	}
	m.st.Consent, m.st.Level, m.st.NudgeDismissed, m.st.DefaultNoticePending = ConsentYes, level, true, false
	if m.st.InstallID == "" {
		m.n++
		m.st.InstallID = strings.Repeat(string(rune('a'+m.n)), 32)
	}
	return m.st, nil
}
func (m *memStore) Disable(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.Consent, m.st.Level, m.st.InstallID, m.st.DefaultNoticePending = ConsentNo, LevelOff, "", false
	m.st.Counts, m.st.Sent, m.st.WindowStart, m.st.DetailedSentAt = Counts{}, Counts{}, nil, nil
	m.disabled++
	return nil
}
func (m *memStore) Reset(context.Context) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Consent == ConsentYes {
		m.n++
		m.st.InstallID = strings.Repeat(string(rune('a'+m.n)), 32)
	}
	return m.st, nil
}
func (m *memStore) Claim(_ context.Context, version string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Consent != ConsentYes || m.st.InstallID == "" {
		return "", false, nil
	}
	since := time.Since(m.claimedAt)
	if since < 20*time.Hour && (m.st.LastSentVersion == version || since < 30*time.Minute) {
		return "", false, nil
	}
	m.claimedAt = time.Now()
	if at := m.st.DetailedSentAt; at != nil && at.UTC().Format(time.DateOnly) < time.Now().UTC().Format(time.DateOnly) {
		if m.st.WindowStart == nil || at.After(*m.st.WindowStart) {
			m.st.WindowStart = at
		}
		m.st.Counts, m.st.Sent, m.st.DetailedSentAt = m.st.Counts.sub(m.st.Sent), Counts{}, nil
	}
	return m.st.InstallID, true, nil
}
func (m *memStore) RecordResult(_ context.Context, s, version string, sent *Counts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.LastStatus = s
	if s != "ok" {
		return nil
	}
	now := time.Now().UTC()
	m.st.LastOKAt = &now
	m.st.LastSentVersion = version
	if sent != nil {
		m.st.Sent, m.st.DetailedSentAt = *sent, &now
	}
	return nil
}
func (m *memStore) RecordKeptDay(_ context.Context, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.LastStatus, m.st.LastSentVersion = "error: build", version
	return nil
}
func (m *memStore) AddHealth(_ context.Context, d Counts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addErr != nil {
		return m.addErr
	}
	if m.st.Level == LevelDetailed {
		m.st.Counts = m.st.Counts.add(d)
	}
	return nil
}
func (m *memStore) DismissNudge(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.NudgeDismissed = m.st.NudgeDismissed || m.st.Level == LevelBasic
	m.st.DefaultNoticePending = false
	return nil
}
func (m *memStore) ApplyDefault(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Level != LevelUnset || m.st.Consent != ConsentUnset {
		return false, nil
	}
	now := time.Now()
	m.n++
	m.st.Consent, m.st.Level, m.st.NudgeDismissed, m.st.DefaultNoticePending = ConsentYes, LevelDetailed, true, true
	m.st.InstallID, m.st.WindowStart = strings.Repeat(string(rune('a'+m.n)), 32), &now
	return true, nil
}
func (m *memStore) Banner(context.Context) (BannerKind, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.st.Level == LevelUnset:
		return BannerUnanswered, nil
	case m.st.DefaultNoticePending && m.st.Level == LevelDetailed:
		return BannerDefaulted, nil
	case m.st.Level == LevelBasic && !m.st.NudgeDismissed:
		return BannerNudge, nil
	}
	return BannerNone, nil
}

type recorder struct {
	reqs   []*http.Request
	bodies []string
	status int
	err    error
}

func (r *recorder) Do(req *http.Request) (*http.Response, error) {
	b, _ := io.ReadAll(req.Body)
	r.reqs = append(r.reqs, req)
	r.bodies = append(r.bodies, string(b))
	if r.err != nil {
		return nil, r.err
	}
	st := r.status
	if st == 0 {
		st = 200
	}
	return &http.Response{StatusCode: st, Body: io.NopCloser(strings.NewReader("{}"))}, nil
}

func rig(t *testing.T, env map[string]string, version string) (*Service, *memStore, *recorder) {
	t.Helper()
	m, r := newMem(), &recorder{}
	s := New(Config{Store: m, Version: version, Getenv: envOf(env), HTTP: r,
		Channel:       func(context.Context) string { return "stable" },
		SetupComplete: func(context.Context) (bool, error) { return true, nil }})
	return s, m, r
}

// defaultRig is rig with the setup wizard reported complete (or not) and an
// audit recorder.
func defaultRig(t *testing.T, env map[string]string, version string, setupDone bool, demo bool) (*Service, *memStore, *recorder, *[]map[string]any) {
	t.Helper()
	m, r := newMem(), &recorder{}
	var audits []map[string]any
	s := New(Config{Store: m, Version: version, Getenv: envOf(env), HTTP: r, Demo: demo,
		Channel:       func(context.Context) string { return "stable" },
		SetupComplete: func(context.Context) (bool, error) { return setupDone, nil }})
	s.SetAudit(func(_ context.Context, action, target string, detail map[string]any) {
		audits = append(audits, map[string]any{"action": action, "target": target, "detail": detail})
	})
	return s, m, r, &audits
}

func TestRunSendsOnlyOnceSetupIsComplete(t *testing.T) {
	ctx := context.Background()
	for _, level := range []Level{LevelDetailed, LevelBasic} {
		done := false
		s, m, r := rig(t, nil, "v1.2.3")
		s.setupComplete = func(context.Context) (bool, error) { return done, nil }
		if err := s.Choose(ctx, level); err != nil {
			t.Fatal(err)
		}
		if err := s.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(r.reqs) != 0 {
			t.Fatalf("%s: %d requests before setup is complete, want 0", level, len(r.reqs))
		}
		if st, _ := m.Load(ctx); st.Level != level {
			t.Fatalf("%s: level = %q", level, st.Level)
		}
		done = true
		if err := s.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(r.reqs) != 1 {
			t.Fatalf("%s: %d requests after setup is complete, want 1", level, len(r.reqs))
		}
	}
}

func TestRunDefaultsToDetailedWhenSetupComplete(t *testing.T) {
	ctx := context.Background()
	s, m, r, audits := defaultRig(t, nil, "v1.2.3", true, false)
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(r.reqs))
	}
	if st, _ := m.Load(ctx); st.Consent != ConsentYes || st.Level != LevelDetailed || !st.DefaultNoticePending {
		t.Fatalf("state = %+v", st)
	}
	if got, _ := s.Banner(ctx); got != BannerDefaulted {
		t.Fatalf("banner = %q, want defaulted", got)
	}
	if len(*audits) != 1 || (*audits)[0]["action"] != "stats.consent" || (*audits)[0]["target"] != "stats" {
		t.Fatalf("audits = %v", *audits)
	}
	d := (*audits)[0]["detail"].(map[string]any)
	if d["choice"] != "detailed" || d["default"] != true {
		t.Fatalf("audit detail = %v", d)
	}
	// A second boot and run change nothing and do not audit again.
	if err := s.Enforce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*audits) != 1 {
		t.Fatalf("audits after second Enforce = %d, want 1", len(*audits))
	}
}

func TestEnforceAtBootAppliesTheDefault(t *testing.T) {
	ctx := context.Background()
	s, m, r, _ := defaultRig(t, nil, "v1.2.3", true, false)
	if err := s.Enforce(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.Load(ctx); st.Level != LevelDetailed || st.Consent != ConsentYes {
		t.Fatalf("state = %+v", st)
	}
	if len(r.reqs) != 0 {
		t.Fatalf("Enforce sent %d requests", len(r.reqs))
	}
}

func TestDefaultNotAppliedWhenSetupIncomplete(t *testing.T) {
	ctx := context.Background()
	s, m, r, audits := defaultRig(t, nil, "v1.2.3", false, false)
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.Load(ctx); st.Level != LevelUnset || len(r.reqs) != 0 || len(*audits) != 0 {
		t.Fatalf("level %q, %d requests, %d audits", st.Level, len(r.reqs), len(*audits))
	}
	// A failing completeness check is "not complete".
	s2 := New(Config{Store: m, Version: "v1.2.3", Getenv: envOf(nil), HTTP: r,
		SetupComplete: func(context.Context) (bool, error) { return true, errors.New("db down") }})
	if applied, err := s2.ApplyDefault(ctx); applied || err == nil {
		t.Fatalf("ApplyDefault on check error = %v, %v", applied, err)
	}
	if st, _ := m.Load(ctx); st.Level != LevelUnset {
		t.Fatalf("level = %q", st.Level)
	}
}

func TestDefaultKeepsExplicitAnswers(t *testing.T) {
	ctx := context.Background()
	for name, level := range map[string]Level{"off": LevelOff, "basic": LevelBasic} {
		s, m, _, audits := defaultRig(t, nil, "v1.2.3", true, false)
		if err := s.Choose(ctx, level); err != nil {
			t.Fatal(err)
		}
		if err := s.Enforce(ctx); err != nil {
			t.Fatal(err)
		}
		if st, _ := m.Load(ctx); st.Level != level || st.DefaultNoticePending || len(*audits) != 0 {
			t.Errorf("%s: state %+v, %d audits", name, st, len(*audits))
		}
	}
}

func TestDefaultNeverAppliesWhenForcedOffDevOrDemo(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		env     map[string]string
		version string
		demo    bool
	}{
		"env off": {map[string]string{EnvTelemetry: "off"}, "v1.2.3", false},
		"dev":     {nil, "dev", false},
		"demo":    {nil, "v1.2.3", true},
	} {
		s, m, r, audits := defaultRig(t, c.env, c.version, true, c.demo)
		if err := s.Run(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		st, _ := m.Load(ctx)
		if len(r.reqs) != 0 || len(*audits) != 0 || st.Level == LevelDetailed || st.DefaultNoticePending {
			t.Errorf("%s: %d requests, %d audits, state %+v", name, len(r.reqs), len(*audits), st)
		}
		if name != "env off" && st.Consent != ConsentUnset {
			t.Errorf("%s: row written: %+v", name, st)
		}
	}
}

func TestChooseClearsTheDefaultedNotice(t *testing.T) {
	ctx := context.Background()
	s, m, _, _ := defaultRig(t, nil, "v1.2.3", true, false)
	for _, level := range []Level{LevelDetailed, LevelBasic, LevelOff} {
		if _, err := s.ApplyDefault(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.Choose(ctx, level); err != nil {
			t.Fatal(err)
		}
		if st, _ := m.Load(ctx); st.DefaultNoticePending {
			t.Fatalf("Choose(%s) left the notice pending", level)
		}
		m.st = State{Consent: ConsentUnset, Level: LevelUnset}
	}
	// "Got it" clears it too.
	if _, err := s.ApplyDefault(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.DismissNudge(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Banner(ctx); got != BannerNone {
		t.Fatalf("banner after dismiss = %q", got)
	}
}

func TestRunSendsNothingWithoutConsent(t *testing.T) {
	ctx := context.Background()
	for name, setup := range map[string]func(*Service, *memStore){
		"unset":  func(s *Service, _ *memStore) { s.setupComplete = nil }, // no default applied
		"no":     func(s *Service, _ *memStore) { _ = s.Choose(ctx, LevelOff) },
		"dev":    func(s *Service, _ *memStore) { _ = s.Choose(ctx, LevelBasic) },
		"forced": func(_ *Service, m *memStore) { _, _ = m.Enable(ctx, LevelBasic) },
	} {
		env, version := map[string]string{}, "v1.2.3"
		switch name {
		case "dev":
			version = "dev"
		case "forced":
			env[EnvTelemetry] = "off"
		}
		s, m, r := rig(t, env, version)
		setup(s, m)
		if err := s.Run(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(r.reqs) != 0 {
			t.Errorf("%s: %d requests, want 0", name, len(r.reqs))
		}
	}
}

func TestRunNeverSendsFromASourceBuild(t *testing.T) {
	ctx := context.Background()
	for _, version := range []string{
		"dev", "v1.4.3-dev.7", "v0.0.0-dev.0", "v1.5.0-rc.1.dev.2", "v1.5.0-pre.dev.3",
		"v0.0.0-20261027123456-abcdef123456", "v1.4.3-0.20261027123456-abcdef123456",
		"v1.4.3+dirty", "1.4.3", "",
	} {
		s, _, r := rig(t, nil, version)
		_ = s.Choose(ctx, LevelBasic)
		if err := s.Run(ctx); err != nil {
			t.Fatalf("%q: %v", version, err)
		}
		if len(r.reqs) != 0 {
			t.Errorf("%q: %d requests, want 0", version, len(r.reqs))
		}
	}
	for _, version := range []string{"v1.5.0", "v1.5.0-rc.1"} {
		s, _, r := rig(t, nil, version)
		_ = s.Choose(ctx, LevelBasic)
		if err := s.Run(ctx); err != nil || len(r.reqs) != 1 {
			t.Errorf("%q: err %v, %d requests, want 1", version, err, len(r.reqs))
		}
	}
}

func TestEnforceDeletesTheID(t *testing.T) {
	ctx := context.Background()
	s, m, r := rig(t, map[string]string{EnvTelemetry: "off"}, "v1.2.3")
	_, _ = m.Enable(ctx, LevelBasic)
	if err := s.Run(ctx); err != nil || len(r.reqs) != 0 {
		t.Fatalf("run: %v, %d requests", err, len(r.reqs))
	}
	if st, _ := m.Load(ctx); st.Consent != ConsentNo || st.InstallID != "" {
		t.Fatalf("env off must leave consent no and no id: %+v", st)
	}
	if err := s.Choose(ctx, LevelBasic); !errors.Is(err, ErrForcedOff) {
		t.Fatalf("choose under env off = %v", err)
	}
	if err := s.Reset(ctx); !errors.Is(err, ErrForcedOff) {
		t.Fatalf("reset under env off = %v", err)
	}
	if st, _ := m.Load(ctx); st.Consent != ConsentNo || st.InstallID != "" {
		t.Fatalf("a refused choice must change nothing: %+v", st)
	}
}

func TestRunSendsOnceWithTheExactBody(t *testing.T) {
	ctx := context.Background()
	s, m, r := rig(t, nil, "v1.2.3")
	if err := s.Choose(ctx, LevelBasic); err != nil {
		t.Fatal(err)
	}
	id := m.st.InstallID
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(r.reqs))
	}
	req := r.reqs[0]
	if req.Method != http.MethodPost || req.URL.String() != DefaultURL {
		t.Errorf("request = %s %s", req.Method, req.URL)
	}
	if req.Header.Get("User-Agent") != "hatamoto-stats" || req.Header.Get("Content-Type") != "application/json" || req.Header.Get("Cookie") != "" {
		t.Errorf("headers = %v", req.Header)
	}
	var got map[string]string
	if err := jsonx.Unmarshal([]byte(r.bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["install"] != id || got["version"] != "v1.2.3" || got["channel"] != "stable" {
		t.Errorf("body = %v", got)
	}
	if m.st.LastStatus != "ok" {
		t.Errorf("status = %q", m.st.LastStatus)
	}
	// A second run inside the interval, as after a restart, sends nothing.
	if err := s.Run(ctx); err != nil || len(r.reqs) != 1 {
		t.Errorf("second run: %v, %d requests", err, len(r.reqs))
	}
}

func TestRunErrorKeepsLastSuccessTime(t *testing.T) {
	ctx := context.Background()
	s, m, r := rig(t, nil, "v1.2.3")
	_ = s.Choose(ctx, LevelBasic)
	if err := s.Run(ctx); err != nil || m.st.LastOKAt == nil {
		t.Fatalf("ok run: %v, last ok %v", err, m.st.LastOKAt)
	}
	first := *m.st.LastOKAt
	m.claimedAt = time.Now().Add(-21 * time.Hour)
	r.status = 404
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if m.st.LastStatus != "error: http 404" || m.st.LastOKAt == nil || !m.st.LastOKAt.Equal(first) {
		t.Errorf("status %q, last ok %v, want unchanged %v", m.st.LastStatus, m.st.LastOKAt, first)
	}
}

func TestRunStatusNeverHoldsTheID(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		status int
		err    error
		want   string
	}{{404, nil, "error: http 404"}, {302, nil, "error: http 302"}, {0, errors.New("dial https://hatamoto.dev: refused"), "error: network"}} {
		s, m, r := rig(t, nil, "v1.2.3")
		r.status, r.err = c.status, c.err
		_ = s.Choose(ctx, LevelBasic)
		id := m.st.InstallID
		if err := s.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if m.st.LastStatus != c.want || strings.Contains(m.st.LastStatus, id) {
			t.Errorf("status = %q, want %q", m.st.LastStatus, c.want)
		}
	}
}

func TestBannerKinds(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		env   map[string]string
		level Level
		want  BannerKind
	}{
		{"unset", nil, LevelUnset, BannerUnanswered},
		{"explicit basic", nil, LevelBasic, BannerNone},
		{"detailed", nil, LevelDetailed, BannerNone},
		{"off", nil, LevelOff, BannerNone},
		{"forced off", map[string]string{EnvTelemetry: "off"}, LevelUnset, BannerNone},
	} {
		s, _, _ := rig(t, c.env, "v1.2.3")
		if c.level != LevelUnset && c.env == nil {
			_ = s.Choose(ctx, c.level)
		}
		if got, err := s.Banner(ctx); err != nil || got != c.want {
			t.Errorf("%s: Banner = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	// A Basic row that was never chosen here (migrated from consent yes) is offered Detailed once.
	s, m, _ := rig(t, nil, "v1.2.3")
	_ = s.Choose(ctx, LevelBasic)
	m.st.NudgeDismissed = false
	if got, _ := s.Banner(ctx); got != BannerNudge {
		t.Fatalf("migrated basic: %q, want nudge", got)
	}
	if err := s.DismissNudge(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Banner(ctx); got != BannerNone {
		t.Fatalf("after dismiss: %q", got)
	}
	f, _, _ := rig(t, map[string]string{EnvTelemetry: "off"}, "v1.2.3")
	if err := f.DismissNudge(ctx); !errors.Is(err, ErrForcedOff) {
		t.Fatalf("dismiss under env off = %v", err)
	}
}

func TestViewLevels(t *testing.T) {
	ctx := context.Background()
	s, m, _ := rig(t, nil, "v1.2.3")
	if v, err := s.View(ctx); err != nil || v.Level != LevelUnset || v.ForcedOff || v.LastSent != nil {
		t.Fatalf("unset view: %+v %v", v, err)
	}
	_ = s.Choose(ctx, LevelDetailed)
	_ = s.Run(ctx)
	if v, _ := s.View(ctx); v.Level != LevelDetailed || v.LastSent != nil && !v.LastSent.Equal(*m.st.LastOKAt) {
		t.Fatalf("detailed view: %+v", v)
	}
	first := m.st.InstallID
	if err := s.Reset(ctx); err != nil || m.st.InstallID == first {
		t.Fatalf("reset: %v", err)
	}
	f, fm, _ := rig(t, map[string]string{EnvTelemetry: "off"}, "v1.2.3")
	_, _ = fm.Enable(ctx, LevelDetailed)
	if v, _ := f.View(ctx); !v.ForcedOff || v.Level != LevelOff {
		t.Fatalf("forced view: %+v", v)
	}
}

func TestChooseRejectsUnknownLevels(t *testing.T) {
	s, m, _ := rig(t, nil, "v1.2.3")
	for _, l := range []Level{LevelUnset, "bogus"} {
		if err := s.Choose(context.Background(), l); err == nil {
			t.Errorf("Choose(%q) accepted", l)
		}
	}
	if m.st.Level != LevelUnset {
		t.Fatalf("state changed: %+v", m.st)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	resp, err := newClient(true).Post(srv.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || hits != 1 {
		t.Fatalf("status %d after %d hits: the redirect was followed", resp.StatusCode, hits)
	}
	c := newClient(false)
	if c.Jar != nil || c.Timeout != 10*time.Second {
		t.Fatalf("jar %v timeout %v", c.Jar, c.Timeout)
	}
}

func detailedRig(t *testing.T) (*Service, *memStore, *recorder, *Health, *fakeSources) {
	t.Helper()
	m, r, h, f := newMem(), &recorder{}, NewHealth(), newFake()
	f.plugins = []PluginInfo{{Name: "searchconsole", Version: "1.0.0", Enabled: true, FirstParty: true}}
	s := New(Config{Store: m, Version: "v1.2.3", Getenv: envOf(nil), HTTP: r, Health: h, Sources: f,
		Channel:       func(context.Context) string { return "stable" },
		SetupComplete: func(context.Context) (bool, error) { return true, nil }})
	return s, m, r, h, f
}

func TestRunBasicBodyIsThreeKeys(t *testing.T) {
	ctx := context.Background()
	s, _, r, h, _ := detailedRig(t)
	_ = s.Choose(ctx, LevelBasic)
	h.Panic()
	if err := s.Run(ctx); err != nil || len(r.bodies) != 1 {
		t.Fatalf("run: %v, %d bodies", err, len(r.bodies))
	}
	entries, err := jsonx.ObjectEntries([]byte(r.bodies[0]))
	if err != nil || len(entries) != 3 || entries[0].Key != "install" || entries[1].Key != "version" || entries[2].Key != "channel" {
		t.Fatalf("basic body: %s (%v)", r.bodies[0], err)
	}
}

func TestRunDetailedBodyCarriesCounts(t *testing.T) {
	ctx := context.Background()
	s, m, r, h, _ := detailedRig(t)
	if err := s.Choose(ctx, LevelDetailed); err != nil {
		t.Fatal(err)
	}
	h.HTTP5xx()
	h.HTTP5xx()
	h.PluginError("searchconsole")
	if err := s.Run(ctx); err != nil || len(r.bodies) != 1 {
		t.Fatalf("run: %v, %d bodies", err, len(r.bodies))
	}
	if err := validateDetailed([]byte(r.bodies[0])); err != nil {
		t.Fatalf("%v\n%s", err, r.bodies[0])
	}
	for _, want := range []string{`"level":"detailed"`, `"http_5xx":2`, `"plugin_errors":{"searchconsole":1}`, `"install":"` + m.st.InstallID} {
		if !strings.Contains(r.bodies[0], want) {
			t.Errorf("missing %s in %s", want, r.bodies[0])
		}
	}
	if m.st.LastStatus != "ok" || m.st.Sent.HTTP5xx != 2 {
		t.Errorf("after ok: status %q sent %+v", m.st.LastStatus, m.st.Sent)
	}
}

func TestRunFailedSendKeepsCountsAndLaterOkRecordsThem(t *testing.T) {
	ctx := context.Background()
	for name, fail := range map[string]func(*recorder){
		"http 500": func(r *recorder) { r.status = 500 },
		"network":  func(r *recorder) { r.err = errors.New("refused") },
	} {
		s, m, r, h, _ := detailedRig(t)
		_ = s.Choose(ctx, LevelDetailed)
		h.Panic()
		fail(r)
		if err := s.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if m.st.Counts.Panics != 1 || m.st.LastOKAt != nil {
			t.Fatalf("%s: failed send lost counts: %+v", name, m.st)
		}
		m.claimedAt = time.Now().Add(-21 * time.Hour)
		*r = recorder{}
		if err := s.Run(ctx); err != nil || len(r.bodies) != 1 || !strings.Contains(r.bodies[0], `"panics":1`) {
			t.Fatalf("%s: retry: %v %v", name, err, r.bodies)
		}
		if m.st.Sent.Panics != 1 {
			t.Fatalf("%s: ok send did not record what it carried: %+v", name, m.st)
		}
	}
}

func TestRunSameDayResendCarriesTheDayTotal(t *testing.T) {
	ctx := context.Background()
	s, m, r, h, _ := detailedRig(t)
	_ = s.Choose(ctx, LevelDetailed)
	h.Panic()
	h.Panic()
	_ = s.Run(ctx) // first send of the day carries 2
	h.Panic()
	// A version change allows a second send the same day.
	m.claimedAt = time.Now().Add(-time.Hour)
	m.st.LastSentVersion = "v1.0.0"
	if err := s.Run(ctx); err != nil || len(r.bodies) != 2 {
		t.Fatalf("second run: %v, %d bodies", err, len(r.bodies))
	}
	if !strings.Contains(r.bodies[0], `"panics":2`) || !strings.Contains(r.bodies[1], `"panics":3`) {
		t.Fatalf("bodies: %v", r.bodies)
	}
	// The next UTC day carries only what came after the last send.
	h.Panic()
	yesterday := time.Now().Add(-24 * time.Hour)
	m.st.DetailedSentAt = &yesterday
	m.claimedAt = time.Now().Add(-21 * time.Hour)
	_ = s.Run(ctx)
	if len(r.bodies) != 3 || !strings.Contains(r.bodies[2], `"panics":1`) {
		t.Fatalf("next day: %d bodies %v", len(r.bodies), r.bodies)
	}
}

func TestChooseDetailedThenRunSendsDetailed(t *testing.T) {
	ctx := context.Background()
	s, _, r, _, _ := detailedRig(t)
	_ = s.Choose(ctx, LevelDetailed)
	_ = s.Run(ctx)
	if len(r.bodies) != 1 || !strings.Contains(r.bodies[0], `"level":"detailed"`) {
		t.Fatalf("bodies: %v", r.bodies)
	}
}

func TestRunSendsNothingWhenOffForcedOrDev(t *testing.T) {
	ctx := context.Background()
	s, m, r, _, _ := detailedRig(t)
	_ = s.Choose(ctx, LevelDetailed)
	_ = s.Choose(ctx, LevelOff)
	_ = s.Run(ctx)
	if len(r.reqs) != 0 {
		t.Fatalf("off sent %d", len(r.reqs))
	}
	f := New(Config{Store: m, Version: "v1.2.3", Getenv: envOf(map[string]string{EnvTelemetry: "off"}), HTTP: r, Sources: newFake()})
	_, _ = m.Enable(ctx, LevelDetailed)
	before := m.disabled
	_ = f.Run(ctx)
	if len(r.reqs) != 0 || m.disabled != before+1 || m.st.Level != LevelOff {
		t.Fatalf("forced off: %d requests, disabled %d->%d, level %q", len(r.reqs), before, m.disabled, m.st.Level)
	}
	d := New(Config{Store: m, Version: "v1.2.3-dev.1", Getenv: envOf(nil), HTTP: r, Sources: newFake()})
	_, _ = m.Enable(ctx, LevelDetailed)
	_ = d.Run(ctx)
	if len(r.reqs) != 0 {
		t.Fatalf("dev build sent %d", len(r.reqs))
	}
}

func TestRunUnreportableDetailedFallsBackToBasicAndMarksTheVersion(t *testing.T) {
	ctx := context.Background()
	for name, mutate := range map[string]func(*fakeSources){
		"theme version": func(f *fakeSources) { f.theme.Version = "1.02.0" },
		"postgres":      func(f *fakeSources) { f.pg = 4 },
	} {
		s, m, r, h, f := detailedRig(t)
		_ = s.Choose(ctx, LevelDetailed)
		h.Panic()
		mutate(f)
		if err := s.Run(ctx); err != nil {
			t.Fatal(err)
		}
		entries, err := jsonx.ObjectEntries([]byte(strings.Join(r.bodies, "")))
		if err != nil || len(r.bodies) != 1 || len(entries) != 3 {
			t.Fatalf("%s: want one basic body, got %v", name, r.bodies)
		}
		if m.st.LastStatus != "ok" || m.st.LastSentVersion != "v1.2.3" || m.st.Counts.Panics != 1 {
			t.Fatalf("%s: status %q version %q counts %+v", name, m.st.LastStatus, m.st.LastSentVersion, m.st.Counts)
		}
		// The version is reported, so the next hourly check stays quiet.
		m.claimedAt = time.Now().Add(-2 * time.Hour)
		_ = s.Run(ctx)
		if len(r.bodies) != 1 {
			t.Fatalf("%s: a reported version was sent again", name)
		}
	}
}

// A Detailed report already went out today; a version-change resend whose
// build fails must not replace it with a Basic ping at the receiver.
func TestRunSameDayBuildFailureKeepsTheDetailedReport(t *testing.T) {
	ctx := context.Background()
	s, m, r, h, f := detailedRig(t)
	_ = s.Choose(ctx, LevelDetailed)
	h.Panic()
	h.Panic()
	h.Panic()
	if err := s.Run(ctx); err != nil || len(r.bodies) != 1 {
		t.Fatalf("first run: %v, %d bodies", err, len(r.bodies))
	}
	f.theme.Version = "1.02.0"
	m.claimedAt = time.Now().Add(-time.Hour)
	m.st.LastSentVersion = "v1.0.0"
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.bodies) != 1 {
		t.Fatalf("a second body was sent: %v", r.bodies)
	}
	if m.st.LastStatus != "error: build" || m.st.LastSentVersion != "v1.2.3" || m.st.Sent.Panics != 3 || m.st.Counts.Panics != 3 {
		t.Fatalf("status %q version %q sent %+v counts %+v", m.st.LastStatus, m.st.LastSentVersion, m.st.Sent, m.st.Counts)
	}
	// The version counts as reported: the next hourly check stays quiet.
	m.claimedAt = time.Now().Add(-2 * time.Hour)
	_ = s.Run(ctx)
	if len(r.bodies) != 1 {
		t.Fatal("the hourly loop sent again")
	}
}

func TestDetailedWithoutSourcesDegradesToBasic(t *testing.T) {
	ctx := context.Background()
	m, r := newMem(), &recorder{}
	s := New(Config{Store: m, Version: "v1.2.3", Getenv: envOf(nil), HTTP: r,
		SetupComplete: func(context.Context) (bool, error) { return true, nil }})
	_ = s.Choose(ctx, LevelDetailed)
	_ = s.Run(ctx)
	entries, err := jsonx.ObjectEntries([]byte(r.bodies[0]))
	if err != nil || len(entries) != 3 {
		t.Fatalf("body: %s", r.bodies[0])
	}
}

func TestFlushStoresAndRestores(t *testing.T) {
	ctx := context.Background()
	s, m, _, h, _ := detailedRig(t)
	_ = s.Choose(ctx, LevelDetailed)
	h.Panic()
	h.PluginError("a")
	m.addErr = errors.New("db down")
	if err := s.Flush(ctx); err == nil {
		t.Fatal("flush error swallowed")
	}
	if !m.st.Counts.IsZero() {
		t.Fatalf("stored on error: %+v", m.st.Counts)
	}
	m.addErr = nil
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if m.st.Counts.Panics != 1 || m.st.Counts.PluginErrors["a"] != 1 {
		t.Fatalf("restored counts lost: %+v", m.st.Counts)
	}
	if c := h.Drain(); !c.IsZero() {
		t.Fatalf("memory not drained: %+v", c)
	}
	if err := New(Config{Store: m}).Flush(ctx); err != nil { // no Health configured
		t.Fatal(err)
	}
}
