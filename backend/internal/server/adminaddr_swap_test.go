package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/hooks"
)

// fakeAddrStore is the persisted admin address without a database.
type fakeAddrStore struct {
	mu     sync.Mutex
	path   string
	by     string
	acked  bool
	ackBy  string // who accepted it; empty means the chooser did
	failOn bool
}

func (f *fakeAddrStore) Get(context.Context) (adminpath.Stored, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := adminpath.Stored{Path: &f.path, SetBy: &f.by}
	if f.acked {
		st.AckBy = &f.by
		if f.ackBy != "" {
			st.AckBy = &f.ackBy
		}
	}
	return st, nil
}

func (f *fakeAddrStore) write(path, by string, ack bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn {
		return errors.New("store down")
	}
	f.path, f.by, f.acked = path, by, ack
	return nil
}
func (f *fakeAddrStore) Ack(_ context.Context, p, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p != f.path {
		return adminpath.ErrNoSiteConfig
	}
	f.acked, f.ackBy = true, by
	return nil
}
func (f *fakeAddrStore) Set(_ context.Context, p, by string) error    { return f.write(p, by, false) }
func (f *fakeAddrStore) SetAck(_ context.Context, p, by string) error { return f.write(p, by, true) }

const swapStart = "/k7qm-x2rf-9tdc"

// swapServer is the wired handler over the route shell with a fake address
// store, the address service on, and the admin at swapStart.
func swapServer(t *testing.T) (http.Handler, *Server, *fakeAddrStore) {
	t.Helper()
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeAddrStore{path: swapStart}
	s.addrStore = store
	s.cfg.EnableCookieAuth = true
	s.cfg.Admin.Resolved = swapStart
	s.validateSession = fakeSession
	s.validateAPICookie = fakeAPISession
	s.hooks = hooks.New(s.logger)
	s.setupRoutes()
	h := s.buildHandler()
	t.Cleanup(func() { s.stopProbes() })
	publicThemeFixture(t, s)
	s.registerNotFound()
	return h, s, store
}

// swapGet is a top-level navigation: the sealed admin refuses a document
// request that does not carry fetch metadata.
func swapGet(h http.Handler, target string) *httptest.ResponseRecorder {
	return swapGetAs(h, target, "document")
}

// swapGetAs is swapGet with another Sec-Fetch-Dest (a plugin frame's script).
func swapGetAs(h http.Handler, target, dest string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", dest)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func strongCandidate(t *testing.T, s *Server) string {
	t.Helper()
	p := (&serverAddress{s: s}).Generate()
	if p == "" {
		t.Fatal("no candidate")
	}
	return p
}

func TestSwapServesNewAddressAndHidesOld(t *testing.T) {
	h, s, store := swapServer(t)
	if rec := swapGet(h, swapStart+"/login"); rec.Code == http.StatusNotFound {
		t.Fatalf("admin not served at the start address: %d", rec.Code)
	}
	next := strongCandidate(t, s)
	res, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: next, By: "Ana"})
	if err != nil || res.Path != next {
		t.Fatalf("Apply = %+v, %v", res, err)
	}
	if store.path != next || store.by != "Ana" || store.acked {
		t.Errorf("store = %+v", store)
	}
	if rec := swapGet(h, next+"/login"); rec.Code == http.StatusNotFound {
		t.Errorf("new address answers %d", rec.Code)
	}
	if s.adminPathValue() != next {
		t.Errorf("adminPathValue = %q", s.adminPathValue())
	}
	// The old address, signed out, is the answer for a path that never existed.
	old := swapGet(h, swapStart+"/login")
	rnd := swapGet(h, "/no-such-page-zq/login")
	if got, want := strings.ReplaceAll(dumpResponse(old), swapStart, "/no-such-page-zq"), dumpResponse(rnd); got != want {
		t.Errorf("old address differs from a random path\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
	if got := s.oldAdminPrefixes(); len(got) != 1 || got[0] != swapStart {
		t.Errorf("old prefixes = %v", got)
	}
	st := (&serverAddress{s: s}).State()
	if st.Path != next || st.SetBy != "Ana" || st.Lock != admin.AddressUnlocked {
		t.Errorf("state = %+v", st)
	}
}

func TestSwapAuthAndPluginUIAreAddressIndependent(t *testing.T) {
	h, s, _ := swapServer(t)
	// The intercept refuses /auth/ as a scanner probe, so go below it.
	h = s.dispatcher()
	check := func(when string) {
		rec := swapGet(h, "/auth/login")
		if rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "/login") {
			t.Errorf("%s: /auth/login = %d %q", when, rec.Code, rec.Header().Get("Location"))
		}
		if rec := swapGetAs(h, "/plugin-ui/_sdk/bridge.js", "script"); rec.Code != http.StatusOK {
			t.Errorf("%s: bridge.js = %d", when, rec.Code)
		}
	}
	check("before")
	next := strongCandidate(t, s)
	if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: next, By: "Ana"}); err != nil {
		t.Fatal(err)
	}
	check("after")
	if loc := swapGet(h, "/auth/login").Header().Get("Location"); !strings.HasPrefix(loc, next+"/") {
		t.Errorf("/auth/login redirects to %q, want the new address %s", loc, next)
	}
}

// The sealed admin's /-/edit link lives at the root; the main mux keeps a
// forwarder for it so a swap does not strand it.
func TestSwapForwardsEditLink(t *testing.T) {
	_, s, _ := swapServer(t)
	req := httptest.NewRequest(http.MethodGet, "/-/edit?id=x", nil)
	if _, pattern := s.mux.Handler(req); pattern != "GET /-/edit" {
		t.Errorf("main mux pattern for /-/edit = %q, want GET /-/edit", pattern)
	}
}

func TestSwapRefusals(t *testing.T) {
	_, s, store := swapServer(t)
	svc := &serverAddress{s: s}
	for name, req := range map[string]admin.ApplyRequest{
		"reserved": {Path: "/api", By: "Ana", Ack: true},
		"invalid":  {Path: "/a b", By: "Ana", Ack: true},
		"weak":     {Path: "/my-place", By: "Ana"},
	} {
		_, err := svc.Apply(context.Background(), req)
		if !errors.Is(err, admin.ErrAddressRefused) {
			t.Errorf("%s: err = %v, want ErrAddressRefused", name, err)
		}
	}
	if store.path != swapStart || s.adminPathValue() != swapStart {
		t.Errorf("a refusal changed the address: %q / %q", store.path, s.adminPathValue())
	}
}

func TestSwapWeakWithAckRecordsAck(t *testing.T) {
	_, s, store := swapServer(t)
	if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: "/my-place", By: "Ana", Ack: true}); err != nil {
		t.Fatal(err)
	}
	if !store.acked || store.path != "/my-place" {
		t.Errorf("store = %+v", store)
	}
	if st := (&serverAddress{s: s}).State(); st.AckBy != "Ana" || st.Level != adminpath.LevelWeak {
		t.Errorf("state = %+v", st)
	}
}

func TestSwapBuildFailureKeepsOldServing(t *testing.T) {
	h, s, store := swapServer(t)
	var fail atomic.Bool
	fail.Store(true)
	s.newAdmin = func(d admin.Deps) (*admin.Admin, error) {
		if fail.Load() {
			return nil, errors.New("template parse")
		}
		return admin.New(d)
	}
	next := strongCandidate(t, s)
	if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: next, By: "Ana"}); err == nil || errors.Is(err, admin.ErrAddressRefused) {
		t.Fatalf("err = %v, want a build error", err)
	}
	if s.renderErr != nil {
		t.Errorf("renderErr = %v, a failed swap must not stop the server", s.renderErr)
	}
	if store.path != swapStart {
		t.Errorf("a failed build stored %q", store.path)
	}
	if rec := swapGet(h, swapStart+"/login"); rec.Code == http.StatusNotFound {
		t.Errorf("old address stopped serving: %d", rec.Code)
	}
	if len(s.oldAdminPrefixes()) != 0 {
		t.Errorf("old prefixes = %v", s.oldAdminPrefixes())
	}
}

func TestSwapStoreFailureKeepsOldServing(t *testing.T) {
	h, s, store := swapServer(t)
	store.failOn = true
	if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: strongCandidate(t, s), By: "Ana"}); err == nil {
		t.Fatal("want an error")
	}
	if s.adminPathValue() != swapStart {
		t.Errorf("live address = %q", s.adminPathValue())
	}
	if rec := swapGet(h, swapStart+"/login"); rec.Code == http.StatusNotFound {
		t.Errorf("old address stopped serving: %d", rec.Code)
	}
}

func TestSwapLocked(t *testing.T) {
	for name, set := range map[string]func(*Server){
		"env":  func(s *Server) { s.cfg.Admin.Path = swapStart },
		"demo": func(s *Server) { s.cfg.Demo.Enabled = true },
	} {
		t.Run(name, func(t *testing.T) {
			_, s, store := swapServer(t)
			set(s)
			svc := &serverAddress{s: s}
			if st := svc.State(); st.Lock == admin.AddressUnlocked || (name == "env" && st.Pinned != "HATAMOTO_ADMIN_PATH") {
				t.Errorf("state = %+v", st)
			}
			if _, err := svc.Apply(context.Background(), admin.ApplyRequest{Path: strongCandidate(t, s), By: "Ana"}); !errors.Is(err, admin.ErrAddressRefused) {
				t.Errorf("Apply err = %v", err)
			}
			if err := svc.Keep(context.Background(), "Ana"); !errors.Is(err, admin.ErrAddressRefused) {
				t.Errorf("Keep err = %v", err)
			}
			if store.path != swapStart || store.acked {
				t.Errorf("store changed: %+v", store)
			}
		})
	}
}

func TestKeepAcknowledgesCurrentAddress(t *testing.T) {
	_, s, store := swapServer(t)
	svc := &serverAddress{s: s}
	store.by = "Bea" // who chose it
	if err := svc.Keep(context.Background(), "Ana"); err != nil {
		t.Fatal(err)
	}
	if !store.acked || store.path != swapStart || s.adminPathValue() != swapStart {
		t.Errorf("store = %+v", store)
	}
	if store.by != "Bea" {
		t.Errorf("Keep overwrote who chose the address: %q", store.by)
	}
	if st := svc.State(); st.AckBy != "Ana" || st.SetBy != "Bea" {
		t.Errorf("state = %+v", st)
	}
}

func TestKeepRefusesWhenStoredAddressDiffersFromLive(t *testing.T) {
	_, s, store := swapServer(t)
	store.path, store.by = "/newer-stored-path-7", "Bea" // changed elsewhere, live is swapStart
	err := (&serverAddress{s: s}).Keep(context.Background(), "Ana")
	if !errors.Is(err, admin.ErrAddressRefused) {
		t.Fatalf("Keep err = %v", err)
	}
	if store.path != "/newer-stored-path-7" || store.by != "Bea" || store.acked {
		t.Errorf("store changed: %+v", store)
	}
}

func TestSwapLateServicesReachNewAdmin(t *testing.T) {
	_, s, _ := swapServer(t)
	if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: strongCandidate(t, s), By: "Ana"}); err != nil {
		t.Fatal(err)
	}
	s.admin.SetUpdates(nil, "no agent")
	s.admin.SetBackups(nil)
	if s.adminDeps.UpdatesUnavailable != "no agent" {
		t.Errorf("deps not updated: %q", s.adminDeps.UpdatesUnavailable)
	}
	// A swap after the late call builds from the retained deps.
	sudo := s.adminDeps.Sudo
	if sudo == nil {
		t.Fatal("no shared sudo store")
	}
	if _, err := (&serverAddress{s: s}).Apply(context.Background(), admin.ApplyRequest{Path: strongCandidate(t, s), By: "Ana"}); err != nil {
		t.Fatal(err)
	}
	if s.adminDeps.Sudo != sudo || s.adminDeps.UpdatesUnavailable != "no agent" {
		t.Error("a swap changed the retained deps")
	}
}

// Requests keep being answered while the address changes, and applies are
// serialised. Run with -race.
func TestSwapConcurrentRequests(t *testing.T) {
	h, s, _ := swapServer(t)
	svc := &serverAddress{s: s}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, p := range []string{s.adminPathValue() + "/login", "/auth/x", "/plugin-ui/_sdk/bridge.js", "/"} {
					swapGet(h, p)
				}
			}
		}()
	}
	var apply sync.WaitGroup
	for range 4 {
		apply.Add(1)
		go func() {
			defer apply.Done()
			for range 3 {
				if _, err := svc.Apply(context.Background(), admin.ApplyRequest{Path: strongCandidate(t, s), By: "Ana"}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	apply.Wait()
	close(stop)
	wg.Wait()
	if rec := swapGet(h, s.adminPathValue()+"/login"); rec.Code == http.StatusNotFound {
		t.Errorf("live address answers %d", rec.Code)
	}
}

// The route table still lists the admin at its address, with a nil-service shell.
func TestAdminRoutesRecordedOnRouteShell(t *testing.T) {
	table, err := RouteTable()
	if err != nil {
		t.Fatal(err)
	}
	var admin, root int
	for _, r := range table {
		switch {
		case strings.HasPrefix(r.Pattern, "GET /admin"):
			admin++
		case r.Pattern == "GET /auth/{rest...}":
			root++
		}
	}
	if admin < 20 || root != 1 {
		t.Errorf("admin routes = %d, /auth/ routes = %d", admin, root)
	}
}

// The session cookie is not bound to the admin address, so a signed-in owner
// stays signed in across a change.
func TestSessionCookieIsNotPrefixBound(t *testing.T) {
	rec := httptest.NewRecorder()
	auth.SetSessionCookie(rec, "tok", false)
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Path != "/" {
		t.Errorf("cookies = %v", c)
	}
}
