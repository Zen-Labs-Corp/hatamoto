package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/user"
)

// editAndRunChangesets serves two changesets: 7, an edit sent for review by the
// author (set by the test), and 42, a plugin's run.
type editAndRunChangesets struct{ requester string }

func (f editAndRunChangesets) Header(_ context.Context, id int64) (changeset.Header, error) {
	exp := time.Now().Add(time.Hour)
	switch id {
	case 7:
		return changeset.Header{ID: 7, State: changeset.StateStaged, ExpiresAt: exp, ItemCount: 1, Kind: changeset.ChangesetEdit, Requester: f.requester}, nil
	case 42:
		return changeset.Header{ID: 42, Plugin: "hello", State: changeset.StateStaged, ExpiresAt: exp, ItemCount: 1, Kind: changeset.ChangesetRun}, nil
	}
	return changeset.Header{}, changeset.ErrNotFound
}

func (editAndRunChangesets) ItemPage(context.Context, int64, int, int) ([]changeset.ItemSummary, error) {
	return nil, nil
}

func (editAndRunChangesets) PreviewSeqs(context.Context, int64) ([]int, error) { return []int{1}, nil }

func newReviewPreviewRig(t *testing.T) (*editQueueRig, *fakePreviewer) {
	t.Helper()
	fp := &fakePreviewer{}
	r := newEditQueueRig(t)
	r.a.deps.Growth.Changesets = editAndRunChangesets{requester: r.author.ID.String()}
	r.a.deps.Growth.ChangesetPreview = fp
	return r, fp
}

// ticketFor mints a frame ticket for the session of token, as a page of the admin would.
func ticketFor(r *editQueueRig, token string, cs int64) string {
	sess := r.sessions.byToken[token]
	tok := r.a.tickets.mint(ticketChangesetPreview, sess.ID, changesetTarget(cs, 1), []byte("http://example.com"))
	return "/admin/changesets/" + strconv.FormatInt(cs, 10) + "/frame/1/" + tok
}

func TestReviewPreviewOfAnEdit(t *testing.T) {
	r, fp := newReviewPreviewRig(t)
	for name, who := range map[string]*user.User{"a decider": r.users[user.RoleReviewer], "the requester": r.author} {
		tok := r.freshToken(who.ID)
		rec := r.do("GET", "/admin/review/7/preview", tok, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: preview page = %d", name, rec.Code)
		}
		m := frameSrcRe.FindStringSubmatch(rec.Body.String())
		if m == nil || !strings.Contains(rec.Body.String(), ` sandbox=""`) || strings.Contains(rec.Body.String(), "allow-") {
			t.Fatalf("%s: no sealed iframe", name)
		}
		if got := r.do("GET", m[1], tok, nil, iframeFrame); got.Code != http.StatusOK {
			t.Errorf("%s: frame = %d, want 200", name, got.Code)
		}
		if got := r.do("GET", m[1], tok, nil, iframeFrame); got.Code != http.StatusNotFound {
			t.Errorf("%s: a ticket is used once, replay = %d", name, got.Code)
		}
	}
	if len(fp.calls) != 2 {
		t.Errorf("renders = %v, want 2", fp.calls)
	}

	// A person who is neither the requester nor a decider gets no page and no frame.
	stranger := r.freshToken(r.users[user.RoleContributor].ID)
	if rec := r.do("GET", "/admin/review/7/preview", stranger, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("stranger: preview page = %d, want 404", rec.Code)
	}
	if got := r.do("GET", ticketFor(r, stranger, 7), stranger, nil, iframeFrame); got.Code != http.StatusNotFound {
		t.Errorf("stranger with a minted ticket: frame = %d, want 404", got.Code)
	}
	other := r.freshToken(r.users[user.RoleReadOnly].ID)
	if got := r.do("GET", ticketFor(r, other, 7), other, nil, iframeFrame); got.Code != http.StatusNotFound {
		t.Errorf("read-only: frame = %d, want 404", got.Code)
	}
}

// A plugin's changeset keeps its owner-only frame check: the widening is for edits.
func TestReviewPreviewDoesNotWidenPluginChangesets(t *testing.T) {
	r, fp := newReviewPreviewRig(t)
	for name, who := range map[string]*user.User{"a decider": r.users[user.RoleReviewer], "the requester of the edit": r.author} {
		tok := r.freshToken(who.ID)
		if got := r.do("GET", ticketFor(r, tok, 42), tok, nil, iframeFrame); got.Code != http.StatusNotFound {
			t.Errorf("%s: plugin changeset frame = %d, want 404", name, got.Code)
		}
	}
	editor := r.freshToken(r.editor.ID)
	if got := r.do("GET", ticketFor(r, editor, 42), editor, nil, iframeFrame); got.Code != http.StatusNotFound {
		t.Errorf("editor: plugin changeset frame = %d, want 404", got.Code)
	}
	if len(fp.calls) != 0 {
		t.Fatalf("a refused frame rendered: %v", fp.calls)
	}
	if got := r.do("GET", ticketFor(r, "owner-token", 42), "owner-token", nil, iframeFrame); got.Code != http.StatusOK {
		t.Errorf("owner: plugin changeset frame = %d, want 200", got.Code)
	}
	// The editor's right to the edit's frame is the decide right, not the plugin one.
	if got := r.do("GET", ticketFor(r, editor, 7), editor, nil, iframeFrame); got.Code != http.StatusOK {
		t.Errorf("editor: edit changeset frame = %d, want 200", got.Code)
	}
}

// With a preview host the cookieless host applies the same check.
func TestReviewPreviewHostChecksTheEditRight(t *testing.T) {
	const host = "preview.example.net"
	r, _ := newReviewPreviewRig(t)
	r.a.deps.PreviewHost = host
	for who, want := range map[uuid.UUID]int{r.users[user.RoleReviewer].ID: http.StatusOK, r.users[user.RoleContributor].ID: http.StatusNotFound} {
		tok := r.freshToken(who)
		r.a.deps.Sessions = liveSessions{fakeSessions: r.sessions, user: who, live: true}
		sess := r.sessions.byToken[tok]
		ticket := r.a.tickets.mint(ticketChangesetPreview, sess.ID, changesetTarget(7, 1), []byte("http://example.com"))
		req := httptest.NewRequest("GET", "/changesets/7/frame/1/"+ticket, nil)
		req.Host = host
		req.Header.Set("Sec-Fetch-Dest", "iframe")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		rec := httptest.NewRecorder()
		r.a.PreviewHostHandler().ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("user %s: preview host = %d, want %d", who, rec.Code, want)
		}
	}
}
