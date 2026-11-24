package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
	"hatamoto.dev/core/internal/spam"
)

type fakeEditor struct {
	c       comment.Comment
	cfg     comment.EntityConfig
	getErr  error
	edited  *comment.Edited
	trashed bool
}

func (f *fakeEditor) Get(context.Context, uuid.UUID) (comment.Comment, error) { return f.c, f.getErr }
func (f *fakeEditor) EntityConfig(context.Context, uuid.UUID) (comment.EntityConfig, error) {
	return f.cfg, nil
}
func (f *fakeEditor) Edit(_ context.Context, _ uuid.UUID, e comment.Edited) (comment.Comment, error) {
	f.edited = &e
	return f.c, nil
}
func (f *fakeEditor) Trash(context.Context, uuid.UUID) (comment.Comment, error) {
	f.trashed = true
	return f.c, nil
}

type editRig struct {
	srv   *Server
	ed    *fakeEditor
	owner uuid.UUID
	now   time.Time
}

func newEditRig() *editRig {
	r := &editRig{owner: uuid.New(), now: time.Now()}
	r.ed = &fakeEditor{c: comment.Comment{
		ID: uuid.New(), EntityID: uuid.New(), CommenterID: &r.owner, Status: comment.StatusApproved,
		CreatedAt: r.now.Add(-time.Minute),
	}}
	r.srv = &Server{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		commentPost: commentPostHooks{
			entity: func(context.Context, uuid.UUID) (string, string, bool) { return "Hello", "/blog/hello", true },
			device: func(_ context.Context, val string) (uuid.UUID, bool) { return r.owner, val == "good" },
		},
	}
	return r
}

// do posts the edit (or delete) with the given cookie and headers.
func (r *editRig) do(del bool, form url.Values, mut ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/-/comments/edit/"+r.ed.c.ID.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "good"})
	req.SetPathValue("commentID", r.ed.c.ID.String())
	for _, m := range mut {
		m(req)
	}
	w := httptest.NewRecorder()
	r.srv.serveOwnComment(w, req, r.ed, del, r.now)
	return w
}

func editForm(body string) url.Values { return url.Values{editBodyField: {body}} }

func TestCommentEditWindow(t *testing.T) {
	r := newEditRig()
	r.ed.c.CreatedAt = r.now.Add(-comment.EditWindow + time.Second) // 14m59s
	w := r.do(false, editForm("A better remark."))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/blog/hello#hm-comments" {
		t.Fatalf("14m59s: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if r.ed.edited == nil || r.ed.edited.ByAdmin || r.ed.edited.BodyRaw != "A better remark." ||
		!strings.Contains(r.ed.edited.BodyHTML, "A better remark.") {
		t.Fatalf("edit = %+v", r.ed.edited)
	}

	r = newEditRig()
	r.ed.c.CreatedAt = r.now.Add(-comment.EditWindow) // exactly 15m
	if w := r.do(false, editForm("late")); w.Code != http.StatusForbidden || r.ed.edited != nil {
		t.Fatalf("15m: %d edited=%v", w.Code, r.ed.edited)
	}
	if w := r.do(true, nil); w.Code != http.StatusForbidden || r.ed.trashed {
		t.Fatalf("delete at 15m: %d trashed=%v", w.Code, r.ed.trashed)
	}
}

func TestCommentEditOwnership(t *testing.T) {
	other := uuid.New()
	for name, mut := range map[string]func(*editRig){
		"other commenter": func(r *editRig) { r.ed.c.CommenterID = &other },
		"no commenter":    func(r *editRig) { r.ed.c.CommenterID = nil },
		"trashed":         func(r *editRig) { r.ed.c.Status = comment.StatusTrash },
	} {
		r := newEditRig()
		mut(r)
		if w := r.do(false, editForm("mine now")); w.Code != http.StatusForbidden || r.ed.edited != nil {
			t.Errorf("%s: %d edited=%v", name, w.Code, r.ed.edited)
		}
		if w := r.do(true, nil); w.Code != http.StatusForbidden || r.ed.trashed {
			t.Errorf("%s: delete %d trashed=%v", name, w.Code, r.ed.trashed)
		}
	}

	r := newEditRig()
	forged := func(req *http.Request) {
		req.Header.Del("Cookie")
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "forged"})
	}
	if w := r.do(false, editForm("x"), forged); w.Code != http.StatusForbidden || r.ed.edited != nil {
		t.Errorf("forged cookie: %d", w.Code)
	}
	noCookie := func(req *http.Request) { req.Header.Del("Cookie") }
	if w := r.do(true, nil, noCookie); w.Code != http.StatusForbidden || r.ed.trashed {
		t.Errorf("no cookie: %d", w.Code)
	}
	// A commenter id in the form is never trusted.
	f := editForm("x")
	f.Set("commenter", r.owner.String())
	r.ed.c.CommenterID = &other
	if w := r.do(false, f); w.Code != http.StatusForbidden {
		t.Errorf("form-supplied owner: %d", w.Code)
	}
}

func TestCommentEditLinkIsPassedAsHasLinks(t *testing.T) {
	r := newEditRig()
	if w := r.do(false, editForm("see https://example.test/x")); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if r.ed.edited == nil || !r.ed.edited.HasLinks || r.ed.edited.ByAdmin ||
		len(r.ed.edited.LinkDomains) != 1 || r.ed.edited.LinkDomains[0] != "example.test" {
		t.Fatalf("edit = %+v", r.ed.edited)
	}
}

func TestCommentEditBodyLimits(t *testing.T) {
	r := newEditRig()
	for body, code := range map[string]string{"   ": "body_empty", strings.Repeat("a", 5001): "body_long"} {
		w := r.do(false, editForm(body))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/blog/hello?cerror="+code+"#hm-comments" {
			t.Errorf("%d runes: %d %q", len(body), w.Code, w.Header().Get("Location"))
		}
	}
	if r.ed.edited != nil {
		t.Fatal("an invalid body was stored")
	}
}

func TestCommentEditDeleteTrashes(t *testing.T) {
	r := newEditRig()
	w := r.do(true, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/blog/hello#hm-comments" || !r.ed.trashed {
		t.Fatalf("delete: %d %q trashed=%v", w.Code, w.Header().Get("Location"), r.ed.trashed)
	}
}

func TestCommentEditCrossSite(t *testing.T) {
	r := newEditRig()
	for name, mut := range map[string]func(*http.Request){
		"fetch site": func(q *http.Request) { q.Header.Set("Sec-Fetch-Site", "cross-site") },
		"origin":     func(q *http.Request) { q.Header.Set("Origin", "https://evil.test") },
		"null origin without fetch site": func(q *http.Request) {
			q.Header.Set("Origin", "null")
			q.Header.Del("Sec-Fetch-Site")
		},
	} {
		if w := r.do(false, editForm("x"), mut); w.Code != http.StatusForbidden || r.ed.edited != nil {
			t.Errorf("%s edit: %d", name, w.Code)
		}
		if w := r.do(true, nil, mut); w.Code != http.StatusForbidden || r.ed.trashed {
			t.Errorf("%s delete: %d", name, w.Code)
		}
	}
}

func TestCommentEditNullOriginSameOriginPasses(t *testing.T) {
	r := newEditRig()
	if w := r.do(false, editForm("x"), func(q *http.Request) { q.Header.Set("Origin", "null") }); w.Code != http.StatusSeeOther || r.ed.edited == nil {
		t.Errorf("same-origin null origin edit: %d", w.Code)
	}
}

func TestCommentEditUnknownComment(t *testing.T) {
	r := newEditRig()
	r.ed.getErr = comment.ErrNotFound
	if w := r.do(false, editForm("x")); w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/-/comments/edit/x", nil)
	req.SetPathValue("commentID", "x")
	r.srv.serveOwnComment(w, req, nil, false, r.now)
	if w.Code != http.StatusNotFound {
		t.Fatalf("no service / bad id: %d", w.Code)
	}
}

// With the comments site switch off the edit and delete routes answer 404,
// like the post route, and change nothing.
func TestCommentEditDeleteSwitchOffIs404(t *testing.T) {
	for _, del := range []bool{false, true} {
		r := newEditRig()
		on := false
		r.srv.commentSlot = &commentSlot{on: func() bool { return on }}
		w := r.do(del, editForm("changed"))
		if w.Code != http.StatusNotFound || r.ed.edited != nil || r.ed.trashed {
			t.Fatalf("del=%v off: status %d edited %v trashed %v", del, w.Code, r.ed.edited, r.ed.trashed)
		}
		on = true
		if w := r.do(del, editForm("changed")); w.Code != http.StatusSeeOther {
			t.Fatalf("del=%v on: status %d", del, w.Code)
		}
	}
}

// holdRig gives the edit rig a submit pipeline whose blocked words hold "viagra".
func (r *editRig) holdRig(t *testing.T) {
	t.Helper()
	set := spam.Defaults()
	set.BlockedWords = []string{"viagra"}
	pc := &postComments{set: comment.Defaults(), mode: comment.ModeOpen, used: map[string]bool{}, keys: comment.NewKeys(postSecret)}
	r.srv.commentSubmit = &commentsubmit.Pipeline{
		Comments: pc, Identity: &postIdentity{},
		Spam:   &spam.Pipeline{Settings: fixedSettings{set}, SecretKey: spam.SecretKey(postSecret)},
		Limits: commentsubmit.NewLimits(time.Now),
	}
	t.Cleanup(r.srv.commentSubmit.Limits.Stop)
}

func TestCommentEditRunsContentChecks(t *testing.T) {
	r := newEditRig()
	r.holdRig(t)
	if w := r.do(false, editForm("cheap viagra here")); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if r.ed.edited == nil || !r.ed.edited.Hold {
		t.Fatalf("a blocked-word edit was not held: %+v", r.ed.edited)
	}
	r.ed.edited = nil
	if w := r.do(false, editForm("a fine rewording")); w.Code != http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if r.ed.edited == nil || r.ed.edited.Hold {
		t.Fatalf("a clean edit was held: %+v", r.ed.edited)
	}
}

func TestCommentEditWindowFollowsTypePolicy(t *testing.T) {
	r := newEditRig()
	r.ed.cfg.Policy = comment.Policy{Enabled: true, EditWindowMinutes: 120}
	r.ed.c.CreatedAt = r.now.Add(-time.Hour) // past the default 15m, inside the type's 120m
	if w := r.do(false, editForm("still mine")); w.Code != http.StatusSeeOther || r.ed.edited == nil {
		t.Fatalf("edit inside the type window: %d edited=%v", w.Code, r.ed.edited)
	}
	if w := r.do(true, nil); w.Code != http.StatusSeeOther || !r.ed.trashed {
		t.Fatalf("delete inside the type window: %d trashed=%v", w.Code, r.ed.trashed)
	}

	r = newEditRig()
	r.ed.cfg.Policy = comment.Policy{Enabled: true, EditWindowMinutes: 30}
	r.ed.c.CreatedAt = r.now.Add(-time.Hour)
	if w := r.do(false, editForm("late")); w.Code != http.StatusForbidden || r.ed.edited != nil {
		t.Fatalf("edit past the type window: %d edited=%v", w.Code, r.ed.edited)
	}
}
