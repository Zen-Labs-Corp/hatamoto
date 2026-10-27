package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/mailsettings"
)

// memStore is an in-memory smtpSettingsStore.
type memStore struct {
	set     mailsettings.Settings
	found   bool
	saves   int
	clears  int
	nextAt  time.Time
	failGet error
}

func (m *memStore) Get(context.Context) (mailsettings.Settings, bool, error) {
	return m.set, m.found, m.failGet
}

func (m *memStore) Save(_ context.Context, s mailsettings.Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	m.nextAt = m.nextAt.Add(time.Second)
	s.UpdatedAt = m.nextAt
	m.set, m.found = s, true
	m.saves++
	return nil
}

func (m *memStore) Clear(context.Context) error {
	m.set, m.found = mailsettings.Settings{}, false
	m.clears++
	return nil
}

func newTestAdapter(t *testing.T, store *memStore) (emailAdmin, *email.Service, *int) {
	t.Helper()
	svc, err := email.NewService(email.Config{}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	var res emailResolution
	applied := 0
	return emailAdmin{
		store: store,
		key:   mailsettings.PasswordKey(testSecret),
		apply: func(ctx context.Context) emailResolution {
			applied++
			cfg, r := resolveEmailConfig(ctx, quietLogger(), config.SMTPConfig{}, store, testSecret, "Site")
			svc.Reload(cfg)
			res = r
			return r
		},
		current: func() emailResolution { return res },
	}, svc, &applied
}

func inputFor(srv *smtptest.Server, password string) admin.EmailInput {
	return admin.EmailInput{
		Provider: mailsettings.ProviderCustom, Host: srv.Host(), Port: srv.Port(),
		Security: email.SecurityNone, Username: "u", Password: password, FromAddress: "from@example.com",
	}
}

func TestEmailAdminSaveAppliesAndNeverExposesPassword(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "secret-pw"})
	store := &memStore{}
	a, svc, applied := newTestAdapter(t, store)
	ctx := context.Background()

	if err := a.Check(ctx, inputFor(sink, "secret-pw"), false); err != nil {
		t.Fatalf("check: %v", err)
	}
	if store.saves != 0 {
		t.Fatal("Check must not store anything")
	}
	if err := a.Save(ctx, inputFor(sink, "secret-pw"), false); err != nil {
		t.Fatalf("save: %v", err)
	}
	if store.saves != 1 || *applied != 1 || !svc.IsConfigured() {
		t.Fatalf("saves=%d applied=%d configured=%v", store.saves, *applied, svc.IsConfigured())
	}
	if string(store.set.PasswordSealed) == "secret-pw" || len(store.set.PasswordSealed) == 0 {
		t.Fatal("password must be stored sealed")
	}

	v, err := a.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Saved || !v.PasswordSaved || v.PasswordLost || v.Source != admin.EmailSourceSaved || !v.Configured ||
		v.Host != sink.Host() || v.Username != "u" {
		t.Fatalf("view = %+v", v)
	}

	// A real send reaches the sink with the saved credentials.
	if err := svc.Send(ctx, []string{"to@example.com"}, "s", "<p>x</p>", "x"); err != nil || len(sink.Messages()) != 1 {
		t.Fatalf("send err=%v messages=%d", err, len(sink.Messages()))
	}
}

func TestEmailAdminKeepPassword(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "secret-pw"})
	store := &memStore{}
	a, _, _ := newTestAdapter(t, store)
	ctx := context.Background()
	if err := a.Save(ctx, inputFor(sink, "secret-pw"), false); err != nil {
		t.Fatal(err)
	}
	sealed := string(store.set.PasswordSealed)

	// Blank password + keep: Check signs in with the saved one, Save carries it over.
	in := inputFor(sink, "")
	if err := a.Check(ctx, in, true); err != nil {
		t.Fatalf("check with kept password: %v", err)
	}
	in.FromName = "Renamed"
	if err := a.Save(ctx, in, true); err != nil {
		t.Fatal(err)
	}
	if string(store.set.PasswordSealed) != sealed || store.set.FromName != "Renamed" {
		t.Fatalf("password not kept: %+v", store.set)
	}

	// Blank without keep means "no password": the sink then refuses MAIL (530).
	var ce *email.CheckError
	if err := a.Check(ctx, inputFor(sink, ""), false); !errors.As(err, &ce) || ce.Code != 530 {
		t.Fatalf("check without password: err = %v, want 530 CheckError", err)
	}

	// Keep with nothing saved is refused.
	empty, _, _ := newTestAdapter(t, &memStore{})
	if err := empty.Save(ctx, inputFor(sink, ""), true); err == nil {
		t.Fatal("keepPassword without a saved password must fail")
	}
}

func TestEmailAdminCheckFailureIsCheckError(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "right"})
	a, _, _ := newTestAdapter(t, &memStore{})
	err := a.Check(context.Background(), inputFor(sink, "wrong"), false)
	var ce *email.CheckError
	if !errors.As(err, &ce) || ce.Code != 535 {
		t.Fatalf("err = %v, want 535 CheckError", err)
	}
}

func TestEmailAdminResetAndLostPassword(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "pw"})
	store := &memStore{}
	a, svc, applied := newTestAdapter(t, store)
	ctx := context.Background()
	if err := a.Save(ctx, inputFor(sink, "pw"), false); err != nil {
		t.Fatal(err)
	}

	// SECRET rotated: the stored password no longer opens.
	rotated := a
	rotated.key = mailsettings.PasswordKey("rotated")
	v, _ := rotated.Get(ctx)
	if v.PasswordSaved || !v.PasswordLost {
		t.Fatalf("view = %+v, want PasswordLost", v)
	}

	if err := a.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if store.clears != 1 || *applied != 2 || svc.IsConfigured() {
		t.Fatalf("clears=%d applied=%d configured=%v", store.clears, *applied, svc.IsConfigured())
	}
	v, _ = a.Get(ctx)
	if v.Saved || v.Source != admin.EmailSourceNone || v.Configured {
		t.Fatalf("view after reset = %+v", v)
	}
}

func TestEmailAdminDisabledFlag(t *testing.T) {
	a, _, _ := newTestAdapter(t, &memStore{})
	t.Setenv("EMAIL_DISABLED", "true")
	if v, _ := a.Get(context.Background()); !v.Disabled {
		t.Fatal("Disabled must reflect EMAIL_DISABLED=true")
	}
}

func TestSyncEmailConvergesOnChangedRow(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "pw"})
	store := &memStore{}
	svc, err := email.NewService(email.Config{}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{logger: quietLogger(), cfg: &config.Config{Secret: testSecret}, mailStore: store, emailService: svc}
	s.applyEmail(context.Background())
	if svc.IsConfigured() {
		t.Fatal("nothing saved yet")
	}

	// Another replica saves a row: the next sync picks it up.
	other := emailAdmin{store: store, key: mailsettings.PasswordKey(testSecret), apply: func(context.Context) emailResolution { return emailResolution{} }, current: s.currentEmailResolution}
	if err := other.Save(context.Background(), inputFor(sink, "pw"), false); err != nil {
		t.Fatal(err)
	}
	s.syncEmail(context.Background())
	if !svc.IsConfigured() || s.currentEmailResolution().Source != emailSourceSaved {
		t.Fatalf("not converged: configured=%v res=%+v", svc.IsConfigured(), s.currentEmailResolution())
	}

	// Another replica clears it.
	_ = store.Clear(context.Background())
	s.syncEmail(context.Background())
	if svc.IsConfigured() || s.currentEmailResolution().Source != emailSourceNone {
		t.Fatalf("clear not converged: res=%+v", s.currentEmailResolution())
	}

	// A read error leaves the live config alone.
	store.failGet = errors.New("db down")
	s.syncEmail(context.Background())
}

func TestRunEmailConvergenceStopsOnCancel(t *testing.T) {
	store := &memStore{}
	svc, _ := email.NewService(email.Config{}, quietLogger())
	s := &Server{logger: quietLogger(), cfg: &config.Config{Secret: testSecret}, mailStore: store, emailService: svc}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.runEmailConvergence(ctx, 5*time.Millisecond); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("convergence loop did not stop")
	}
}

// A request that is cancelled after the commit must still re-apply the saved
// row, under a bounded context.
func TestEmailAdminApplyIgnoresRequestCancellation(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "pw"})
	store := &memStore{}
	a, _, _ := newTestAdapter(t, store)
	var gotErr error
	var hasDeadline bool
	inner := a.apply
	a.apply = func(ctx context.Context) emailResolution {
		gotErr = ctx.Err()
		_, hasDeadline = ctx.Deadline()
		return inner(ctx)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Save(ctx, inputFor(sink, "pw"), false); err != nil {
		t.Fatal(err)
	}
	if gotErr != nil || !hasDeadline {
		t.Fatalf("apply ctx: err=%v hasDeadline=%v; want live and bounded", gotErr, hasDeadline)
	}
	if err := a.Reset(ctx); err != nil || gotErr != nil {
		t.Fatalf("reset: err=%v ctxErr=%v", err, gotErr)
	}
}

// A read error after a configuration is live keeps it; at boot it falls back to env.
func TestApplyEmailKeepsLiveConfigOnRuntimeReadError(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "pw"})
	store := &memStore{}
	svc, err := email.NewService(email.Config{}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{logger: quietLogger(), cfg: &config.Config{Secret: testSecret}, mailStore: store, emailService: svc}
	other := emailAdmin{store: store, key: mailsettings.PasswordKey(testSecret)}
	if err := store.Save(context.Background(), mustSettings(t, other, sink)); err != nil {
		t.Fatal(err)
	}
	s.applyEmail(context.Background())
	if !svc.IsConfigured() {
		t.Fatal("saved row not applied")
	}
	want := s.currentEmailResolution()

	store.failGet = errors.New("db down")
	res := s.applyEmail(context.Background())
	if !svc.IsConfigured() || res != want || s.currentEmailResolution() != want {
		t.Fatalf("live config dropped on a read error: configured=%v res=%+v", svc.IsConfigured(), res)
	}
}

func mustSettings(t *testing.T, a emailAdmin, sink *smtptest.Server) mailsettings.Settings {
	t.Helper()
	set, _, err := a.settings(context.Background(), inputFor(sink, "pw"), false)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// gatedStore blocks its first Get until released, to interleave two applies.
type gatedStore struct {
	memStore
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (g *gatedStore) Get(ctx context.Context) (mailsettings.Settings, bool, error) {
	g.mu.Lock()
	g.calls++
	first := g.calls == 1
	set, found := g.set, g.found
	g.mu.Unlock()
	if first {
		close(g.started)
		<-g.release
	}
	return set, found, nil
}

// An apply that read the old row must not overwrite a newer apply.
func TestApplyEmailConcurrentApplyEndsOnNewestRow(t *testing.T) {
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "pw"})
	store := &gatedStore{started: make(chan struct{}), release: make(chan struct{})}
	svc, err := email.NewService(email.Config{}, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{logger: quietLogger(), cfg: &config.Config{Secret: testSecret}, mailStore: store, emailService: svc}
	other := emailAdmin{store: &store.memStore, key: mailsettings.PasswordKey(testSecret)}

	old := mustSettings(t, other, sink)
	old.FromAddress, old.UpdatedAt = "old@example.com", time.Unix(1, 0)
	store.set, store.found = old, true

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.applyEmail(context.Background()) }() // reads the old row, then waits
	<-store.started
	newer := old
	newer.FromAddress, newer.UpdatedAt = "new@example.com", time.Unix(2, 0)
	store.mu.Lock()
	store.set = newer
	store.mu.Unlock()
	go func() { defer wg.Done(); s.applyEmail(context.Background()) }()
	time.Sleep(100 * time.Millisecond) // lets the second apply finish first when nothing serialises them
	close(store.release)
	wg.Wait()

	if got := s.currentEmailResolution().SavedAt; !got.Equal(newer.UpdatedAt) {
		t.Fatalf("recorded SavedAt = %v, want the newest", got)
	}
	if got := svc.FromAddress(); got != "new@example.com" {
		t.Fatalf("live From = %q, want new@example.com", got)
	}
}

// Keeping the saved password is refused unless host and username match the
// saved row, and nothing is dialled.
func TestEmailAdminKeepPasswordRequiresSameHostAndUsername(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")
	sink := smtptest.Start(t, smtptest.Options{User: "u", Pass: "pw"})
	store := &memStore{}
	a, _, _ := newTestAdapter(t, store)
	if err := a.Save(context.Background(), inputFor(sink, "pw"), false); err != nil {
		t.Fatal(err)
	}
	connects := len(sink.Messages())

	for name, mutate := range map[string]func(*admin.EmailInput){
		"host":     func(in *admin.EmailInput) { in.Host = "evil.example" },
		"username": func(in *admin.EmailInput) { in.Username = "other" },
	} {
		in := inputFor(sink, "")
		mutate(&in)
		if err := a.Check(context.Background(), in, true); err == nil {
			t.Fatalf("%s changed: Check kept the saved password", name)
		}
		if err := a.Save(context.Background(), in, true); err == nil {
			t.Fatalf("%s changed: Save kept the saved password", name)
		}
	}
	if len(sink.Messages()) != connects || store.saves != 1 {
		t.Fatalf("a refused keep must not send or save (saves=%d)", store.saves)
	}
	// Same host and username still keeps it.
	if err := a.Check(context.Background(), inputFor(sink, ""), true); err != nil {
		t.Fatalf("unchanged host/username: %v", err)
	}
}
