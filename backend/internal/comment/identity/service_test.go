package identity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/email"
)

var testNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type fakeComments struct {
	fakeBlocks
	confirmErr error
	confirmed  []uuid.UUID
	settings   comment.Settings
	setErr     error
}

func (f *fakeComments) Confirm(_ context.Context, id uuid.UUID) (comment.Comment, error) {
	f.confirmed = append(f.confirmed, id)
	return comment.Comment{ID: id, Status: comment.StatusApproved}, f.confirmErr
}

func (f *fakeComments) EnsureCommenter(context.Context, string) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (f *fakeComments) Settings(context.Context) (comment.Settings, error) {
	return f.settings, f.setErr
}

type sent struct {
	to string
	d  email.CommentConfirmationData
}

type signInSent struct {
	to string
	d  email.CommentSignInData
}

type fakeMailer struct {
	calls   []sent
	signIns []signInSent
	err     error
}

func (f *fakeMailer) SendCommentSignIn(_ context.Context, to string, d email.CommentSignInData) error {
	f.signIns = append(f.signIns, signInSent{to, d})
	return f.err
}

func (f *fakeMailer) SendCommentConfirmation(_ context.Context, to string, d email.CommentConfirmationData) error {
	f.calls = append(f.calls, sent{to, d})
	return f.err
}

// fakeStore applies the real limit arithmetic over an in-memory list.
type fakeStore struct {
	rows               []issueRow
	issueN             int
	versions           map[uuid.UUID]int
	versionQs          int
	peekOK             bool
	released           int
	releaseHadDeadline bool
	versionErr         error
}

func (f *fakeStore) Issue(_ context.Context, r issueRow, l Limits) (bool, error) {
	f.issueN++
	var c, ip, net int
	for _, o := range f.rows {
		if o.CommenterID == r.CommenterID {
			c++
		}
		if r.IPHash != "" && o.IPHash == r.IPHash {
			ip++
		}
		if r.NetHash != "" && o.NetHash == r.NetHash {
			net++
		}
	}
	if c >= l.PerCommenter || ip >= l.PerIP || net >= l.PerNet {
		return true, nil
	}
	f.rows = append(f.rows, r)
	return false, nil
}

func (f *fakeStore) Peek(context.Context, []byte, time.Time) (a, b, c uuid.UUID, ok bool, err error) {
	return uuid.New(), uuid.New(), uuid.New(), f.peekOK, nil
}

func (f *fakeStore) PeekSignIn(context.Context, []byte, time.Time) (uuid.UUID, bool, error) {
	return uuid.New(), f.peekOK, nil
}

func (f *fakeStore) Consume(context.Context, []byte, time.Time, bool) (a, b uuid.UUID, ok bool, err error) {
	return uuid.New(), uuid.New(), true, nil
}

func (f *fakeStore) Release(ctx context.Context, _ []byte) error {
	f.released++
	_, f.releaseHadDeadline = ctx.Deadline()
	return nil
}

func (f *fakeStore) TokenVersion(_ context.Context, id uuid.UUID) (int, bool, error) {
	f.versionQs++
	if f.versionErr != nil {
		return 0, false, f.versionErr
	}
	v, ok := f.versions[id]
	return v, ok, nil
}

func (f *fakeStore) Purge(context.Context, time.Time) (int64, int64, int64, error) {
	return 0, 0, 0, nil
}

type env struct {
	svc *Service
	st  *fakeStore
	m   *fakeMailer
	c   *fakeComments
	// site name and base returned by the SiteInfo
	base string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{st: &fakeStore{versions: map[uuid.UUID]int{}}, m: &fakeMailer{}, c: &fakeComments{}, base: "https://example.test/"}
	e.c.settings = comment.Defaults()
	e.c.settings.Enabled = true
	e.svc = &Service{
		comments: e.c, mailer: e.m, store: e.st,
		site:   func(context.Context) (string, string) { return "Site S", e.base },
		key:    DeviceKey("unit-test-secret"),
		limits: DefaultLimits,
		now:    func() time.Time { return testNow },
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return e
}

func draft(cid uuid.UUID) comment.Comment {
	return comment.Comment{ID: uuid.New(), EntityID: uuid.New(), CommenterID: &cid, Status: comment.StatusDraft, IPHash: "iph", NetHash: "neth"}
}

func request(c comment.Comment) ConfirmRequest {
	return ConfirmRequest{Comment: c, Email: "a@example.test", EntityTitle: "A title"}
}

func TestRequestConfirmation_FourthWithin24hSendsNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cid := uuid.New()
	for i := 0; i < 4; i++ {
		out, err := e.svc.RequestConfirmation(context.Background(), request(draft(cid)))
		if out != OutcomeCheckEmail || err != nil {
			t.Fatalf("call %d: (%v, %v)", i, out, err)
		}
	}
	if len(e.m.calls) != 3 {
		t.Errorf("mails = %d, want 3", len(e.m.calls))
	}
}

func TestRequestConfirmation_IPAndNetLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.svc.limits = Limits{PerCommenter: 100, PerIP: 2, PerNet: 100}
	for i := 0; i < 3; i++ {
		d := draft(uuid.New())
		if out, err := e.svc.RequestConfirmation(context.Background(), request(d)); out != OutcomeCheckEmail || err != nil {
			t.Fatalf("(%v, %v)", out, err)
		}
	}
	if len(e.m.calls) != 2 {
		t.Errorf("ip limit: mails = %d, want 2", len(e.m.calls))
	}
	if r := e.st.rows[0]; r.IPHash != "iph" || r.NetHash != "neth" {
		t.Errorf("store got hashes %q/%q", r.IPHash, r.NetHash)
	}

	e = newEnv(t)
	e.svc.limits = Limits{PerCommenter: 100, PerIP: 100, PerNet: 1}
	for i := 0; i < 2; i++ {
		if out, err := e.svc.RequestConfirmation(context.Background(), request(draft(uuid.New()))); out != OutcomeCheckEmail || err != nil {
			t.Fatalf("(%v, %v)", out, err)
		}
	}
	if len(e.m.calls) != 1 {
		t.Errorf("net limit: mails = %d, want 1", len(e.m.calls))
	}
}

func TestRequestConfirmation_MailCarriesNoCommenterText(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	d := draft(uuid.New())
	d.AuthorName, d.BodyRaw = "SENTINEL_NAME", "SENTINEL_BODY"
	if _, err := e.svc.RequestConfirmation(context.Background(), request(d)); err != nil {
		t.Fatal(err)
	}
	if len(e.m.calls) != 1 {
		t.Fatalf("mails = %d", len(e.m.calls))
	}
	got := fmt.Sprintf("%+v", e.m.calls[0].d)
	if strings.Contains(got, "SENTINEL") {
		t.Errorf("mail data leaks commenter text: %s", got)
	}
	prefix := "https://example.test/-/comments/confirm/"
	url := e.m.calls[0].d.URL
	if !strings.HasPrefix(url, prefix) || len(url)-len(prefix) != 43 {
		t.Errorf("URL = %q", url)
	}
	if e.m.calls[0].to != "a@example.test" || e.m.calls[0].d.SiteName != "Site S" || e.m.calls[0].d.EntityTitle != "A title" {
		t.Errorf("data = %+v", e.m.calls[0])
	}
}

func TestRequestConfirmation_SendFailureSameOutcome(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.m.err = errors.New("smtp down")
	out, err := e.svc.RequestConfirmation(context.Background(), request(draft(uuid.New())))
	if out != OutcomeCheckEmail || err != nil {
		t.Errorf("(%v, %v)", out, err)
	}
	if e.st.issueN != 1 {
		t.Errorf("issue calls = %d, want 1 (a failed send still counts)", e.st.issueN)
	}
}

func TestRequestConfirmation_NoBaseURLSendsNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.base = ""
	out, err := e.svc.RequestConfirmation(context.Background(), request(draft(uuid.New())))
	if out != OutcomeCheckEmail || err != nil {
		t.Errorf("(%v, %v)", out, err)
	}
	if len(e.m.calls) != 0 || e.st.issueN != 0 {
		t.Errorf("mails %d, issues %d; want none", len(e.m.calls), e.st.issueN)
	}
}

func TestRequestConfirmation_DisposableRejected(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.c.listed = []string{"example.test"}
	out, err := e.svc.RequestConfirmation(context.Background(), request(draft(uuid.New())))
	if !errors.Is(err, ErrEmailNotAccepted) || out != 0 {
		t.Errorf("(%v, %v)", out, err)
	}
	if len(e.m.calls) != 0 || e.st.issueN != 0 {
		t.Errorf("mails %d, issues %d; want none", len(e.m.calls), e.st.issueN)
	}
}

func TestRequestConfirmation_NotDraft(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	d := draft(uuid.New())
	d.Status = comment.StatusPending
	if _, err := e.svc.RequestConfirmation(context.Background(), request(d)); !errors.Is(err, ErrNotDraft) {
		t.Errorf("pending: err = %v", err)
	}
	d = draft(uuid.New())
	d.CommenterID = nil
	if _, err := e.svc.RequestConfirmation(context.Background(), request(d)); !errors.Is(err, ErrNotDraft) {
		t.Errorf("no commenter: err = %v", err)
	}
}

func TestDevice_TokenVersionBumpInvalidates(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cid := uuid.New()
	val := SignDevice(e.svc.key, DeviceClaims{CommenterID: cid, TokenVersion: 1, Expires: testNow.Add(time.Hour)})
	e.st.versions[cid] = 1
	if got, ok := e.svc.Device(context.Background(), val); !ok || got != cid {
		t.Errorf("version 1: (%v, %v)", got, ok)
	}
	e.st.versions[cid] = 2
	if _, ok := e.svc.Device(context.Background(), val); ok {
		t.Error("bumped version still accepted")
	}
	delete(e.st.versions, cid)
	if _, ok := e.svc.Device(context.Background(), val); ok {
		t.Error("unknown commenter accepted")
	}
}

func TestDevice_ForgedCookieRejected(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cid := uuid.New()
	e.st.versions[cid] = 1
	val := SignDevice(e.svc.key, DeviceClaims{CommenterID: cid, TokenVersion: 1, Expires: testNow.Add(time.Hour)})
	for _, bad := range []string{val + "x", strings.Replace(val, ".1.", ".2.", 1), "", "garbage"} {
		if _, ok := e.svc.Device(context.Background(), bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if e.st.versionQs != 0 {
		t.Errorf("store queried %d times for bad MACs", e.st.versionQs)
	}
}

func validToken(t *testing.T) string {
	t.Helper()
	raw, _, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestConfirm_MapsModelErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		in      error
		invalid bool
	}{
		{"not found", comment.ErrNotFound, true},
		{"bad transition", comment.ErrBadTransition, true},
		{"wrapped", fmt.Errorf("x: %w", comment.ErrBadTransition), true},
		{"other", errors.New("boom"), false},
	} {
		e := newEnv(t)
		e.c.confirmErr = tc.in
		e.svc.store = &versionAll{fakeStore: e.st, v: 1}
		_, err := e.svc.Confirm(context.Background(), validToken(t))
		if tc.invalid && !errors.Is(err, ErrTokenInvalid) {
			t.Errorf("%s: err = %v, want ErrTokenInvalid", tc.name, err)
		}
		if wantRelease := !tc.invalid; (e.st.released == 1) != wantRelease {
			t.Errorf("%s: released = %d, want release = %v", tc.name, e.st.released, wantRelease)
		}
		if !tc.invalid && (err == nil || errors.Is(err, ErrTokenInvalid) || err.Error() != "boom") {
			t.Errorf("%s: err = %v, want unchanged", tc.name, err)
		}
	}
	if _, err := newEnv(t).svc.Confirm(context.Background(), "short"); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("malformed token: %v", err)
	}
}

func TestConfirm_SetsCookieClaims(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.c.settings.DeviceCookieDays = 90
	cid := uuid.New()
	// fakeStore.Consume returns a random commenter, so look its version up by any id.
	e.st.versions = nil
	e.svc.store = &versionAll{fakeStore: e.st, v: 1}
	c, err := e.svc.Confirm(context.Background(), validToken(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.CookieLifetime != 90*24*time.Hour || c.TokenVersion != 1 {
		t.Errorf("confirmed = %+v", c)
	}
	c.CommenterID = cid
	for _, secure := range []bool{true, false} {
		ck := e.svc.Cookie(c, secure)
		if ck.Name != "hm_c" || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" ||
			ck.MaxAge != 90*24*3600 || ck.Secure != secure {
			t.Errorf("cookie = %+v", ck)
		}
		claims, ok := VerifyDevice(e.svc.key, ck.Value, testNow)
		if !ok || claims.CommenterID != cid || claims.TokenVersion != 1 {
			t.Errorf("claims = %+v, %v", claims, ok)
		}
	}
}

// versionAll answers every TokenVersion lookup with v.
type versionAll struct {
	*fakeStore
	v int
}

func (s *versionAll) TokenVersion(context.Context, uuid.UUID) (int, bool, error) {
	return s.v, true, nil
}

func TestEnabled_FailsClosed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if !e.svc.Enabled(context.Background()) {
		t.Fatal("enabled settings reported off")
	}
	e.c.settings = comment.Defaults()
	e.c.setErr = errors.New("db down")
	e.c.settings.Enabled = true // even a stray true must not win over an error
	if e.svc.Enabled(context.Background()) {
		t.Error("Enabled with a settings error")
	}
}

func TestNew_EmptySecretFails(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Error("New with empty config succeeded")
	}
	if _, err := New(Config{Secret: ""}); err == nil {
		t.Error("New with empty secret succeeded")
	}
}

func TestConfirm_ReleasesTokenOnTransientVersionError(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.st.versionErr = errors.New("boom")
	if _, err := e.svc.Confirm(context.Background(), validToken(t)); err == nil || errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("err = %v, want the transient error", err)
	}
	if !e.st.releaseHadDeadline {
		t.Error("Release ran without a deadline")
	}
	if e.st.released != 1 || len(e.c.confirmed) != 0 {
		t.Errorf("released = %d, confirmed = %d; want the token released before any confirm", e.st.released, len(e.c.confirmed))
	}
}

func TestClearCookie(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, secure := range []bool{true, false} {
		c := e.svc.ClearCookie(secure)
		if c.Name != CookieName || c.Value != "" || c.Path != "/" || c.MaxAge != -1 ||
			!c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != secure {
			t.Errorf("ClearCookie(%v) = %+v", secure, c)
		}
	}
}

func TestRequestSignIn_SameOutcomeAndLink(t *testing.T) {
	e := newEnv(t)
	out, err := e.svc.RequestSignIn(context.Background(), SignInRequest{Email: "a@example.test", IPHash: "ip"})
	if out != OutcomeCheckEmail || err != nil || len(e.m.signIns) != 1 {
		t.Fatalf("(%v, %v), mails %d", out, err, len(e.m.signIns))
	}
	if u := e.m.signIns[0].d.URL; !strings.HasPrefix(u, "https://example.test/-/comments/signin/") {
		t.Errorf("url = %q", u)
	}
	e.base = ""
	if out, err := e.svc.RequestSignIn(context.Background(), SignInRequest{Email: "a@example.test"}); out != OutcomeCheckEmail || err != nil || len(e.m.signIns) != 1 {
		t.Errorf("no base: (%v, %v), mails %d", out, err, len(e.m.signIns))
	}
}
