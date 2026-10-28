package installstats

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore mirrors the Store's rules in memory.
type memStore struct {
	mu        sync.Mutex
	st        State
	claimedAt time.Time
	n         int
}

func newMem() *memStore { return &memStore{st: State{Consent: ConsentUnset}} }

func (m *memStore) Load(context.Context) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st, nil
}
func (m *memStore) Enable(context.Context) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.Consent = ConsentYes
	if m.st.InstallID == "" {
		m.n++
		m.st.InstallID = strings.Repeat(string(rune('a'+m.n)), 32)
	}
	return m.st, nil
}
func (m *memStore) Disable(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.Consent, m.st.InstallID = ConsentNo, ""
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
func (m *memStore) Claim(context.Context) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Consent != ConsentYes || m.st.InstallID == "" || time.Since(m.claimedAt) < 20*time.Hour {
		return "", false, nil
	}
	m.claimedAt = time.Now()
	return m.st.InstallID, true, nil
}
func (m *memStore) RecordResult(_ context.Context, s string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.LastStatus = s
	return nil
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
		Channel: func(context.Context) string { return "stable" }})
	return s, m, r
}

func TestRunSendsNothingWithoutConsent(t *testing.T) {
	ctx := context.Background()
	for name, setup := range map[string]func(*Service, *memStore){
		"unset":  func(*Service, *memStore) {},
		"no":     func(s *Service, _ *memStore) { _ = s.Choose(ctx, false) },
		"dev":    func(s *Service, _ *memStore) { _ = s.Choose(ctx, true) },
		"forced": func(_ *Service, m *memStore) { _, _ = m.Enable(ctx) },
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

func TestEnforceDeletesTheID(t *testing.T) {
	ctx := context.Background()
	s, m, r := rig(t, map[string]string{EnvTelemetry: "off"}, "v1.2.3")
	_, _ = m.Enable(ctx)
	if err := s.Run(ctx); err != nil || len(r.reqs) != 0 {
		t.Fatalf("run: %v, %d requests", err, len(r.reqs))
	}
	if st, _ := m.Load(ctx); st.Consent != ConsentNo || st.InstallID != "" {
		t.Fatalf("env off must leave consent no and no id: %+v", st)
	}
	if err := s.Choose(ctx, true); !errors.Is(err, ErrForcedOff) {
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
	if err := s.Choose(ctx, true); err != nil {
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
	if err := json.Unmarshal([]byte(r.bodies[0]), &got); err != nil {
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

func TestRunStatusNeverHoldsTheID(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		status int
		err    error
		want   string
	}{{404, nil, "error: http 404"}, {302, nil, "error: http 302"}, {0, errors.New("dial https://hatamoto.dev: refused"), "error: network"}} {
		s, m, r := rig(t, nil, "v1.2.3")
		r.status, r.err = c.status, c.err
		_ = s.Choose(ctx, true)
		id := m.st.InstallID
		if err := s.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if m.st.LastStatus != c.want || strings.Contains(m.st.LastStatus, id) {
			t.Errorf("status = %q, want %q", m.st.LastStatus, c.want)
		}
	}
}

func TestViewPreview(t *testing.T) {
	ctx := context.Background()
	s, m, _ := rig(t, nil, "v1.2.3")
	v, err := s.View(ctx)
	if err != nil || v.State.Consent != ConsentUnset || v.Preview.Install != placeholderID || v.ForcedOff {
		t.Fatalf("unset view: %+v %v", v, err)
	}
	_ = s.Choose(ctx, true)
	v, _ = s.View(ctx)
	if v.Preview.Install != m.st.InstallID || !strings.Contains(v.PreviewJSON, `"channel": "stable"`) {
		t.Fatalf("yes view: %+v", v)
	}
	first := m.st.InstallID
	if err := s.Reset(ctx); err != nil || m.st.InstallID == first {
		t.Fatalf("reset: %v", err)
	}
	f, _, _ := rig(t, map[string]string{EnvTelemetry: "off"}, "v1.2.3")
	if v, _ := f.View(ctx); !v.ForcedOff || v.State.Consent != ConsentNo {
		t.Fatalf("forced view: %+v", v)
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
