package admin

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const reviewBase = "/admin/plugins/hello/changesets/42"

func grantSudo(g *rig) { g.a.sudo.store.Grant(g.sessions.byToken["owner-token"].ID) }

func TestReviewDecide(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	if rec := g.do("POST", reviewBase+"/decide", "editor-token", url.Values{"item_3": {"approved"}}, nil); rec.Code != 403 || len(fr.decides) != 0 {
		t.Fatalf("editor: %d", rec.Code)
	}
	form := url.Values{"item_3": {"approved"}, "item_4": {"dropped"}, "item_9": {"dropped"}, "group_redirects": {"dropped"}}
	rec := g.do("POST", reviewBase+"/decide", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != reviewBase {
		t.Fatalf("decide: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	want := []decideCall{{42, []int{3}, "approved"}, {42, []int{4, 9, 10, 11}, "dropped"}}
	if !reflect.DeepEqual(fr.decides, want) {
		t.Errorf("decides = %+v, want %+v", fr.decides, want)
	}
	for _, bad := range []url.Values{{"item_3": {"nope"}}, {"item_x": {"approved"}}, {"group_redirects": {"rejected"}}} {
		if rec := g.do("POST", reviewBase+"/decide", "owner-token", bad, nil); rec.Code != 400 {
			t.Errorf("%v: %d, want 400", bad, rec.Code)
		}
	}
	fr.err = ErrReviewNotStaged
	if rec := g.do("POST", reviewBase+"/decide", "owner-token", form, nil); rec.Code != 409 {
		t.Errorf("not staged: %d", rec.Code)
	}
	g2 := newRig(t)
	if rec := g2.do("POST", reviewBase+"/decide", "owner-token", form, nil); rec.Code != 503 {
		t.Errorf("not wired: %d", rec.Code)
	}
}

func TestReviewSuggestionsNeedSudo(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	form := url.Values{"suggestion_7": {"approved"}, "suggestion_8": {"rejected"}}
	// An editor is refused before any password prompt.
	if rec := g.do("POST", reviewBase+"/suggestions", "editor-token", form, nil); rec.Code != 403 {
		t.Fatalf("editor: %d", rec.Code)
	}
	// An owner without a fresh password is sent to confirm and the POST is not replayed.
	rec := g.do("POST", reviewBase+"/suggestions", "owner-token", form, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), g.a.sudoPath()) || len(fr.suggests) != 0 {
		t.Fatalf("no sudo: %d %s %d calls", rec.Code, rec.Header().Get("Location"), len(fr.suggests))
	}
	grantSudo(g)
	rec = g.do("POST", reviewBase+"/suggestions", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != reviewBase {
		t.Fatalf("sudo: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if want := []suggestCall{{42, 7, "approved"}, {42, 8, "rejected"}}; !reflect.DeepEqual(fr.suggests, want) {
		t.Errorf("suggests = %+v", fr.suggests)
	}
	if len(events) != 2 || events[0].Action != "changeset.suggestion.approve" || events[1].Action != "changeset.suggestion.reject" {
		t.Errorf("audit = %+v", events)
	}
	if rec := g.do("POST", reviewBase+"/suggestions", "owner-token", url.Values{"suggestion_7": {"dropped"}}, nil); rec.Code != 400 {
		t.Errorf("bad decision: %d", rec.Code)
	}
}

func TestReviewPublishAndDiscard(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	var events []AuditEvent
	g.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	if rec := g.do("POST", reviewBase+"/publish", "editor-token", nil, nil); rec.Code != 403 || fr.publishes != 0 {
		t.Fatalf("editor publish: %d", rec.Code)
	}
	fr.publishErr = ErrReviewUndecided
	rec := g.do("POST", reviewBase+"/publish", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != reviewBase || len(events) != 0 {
		t.Errorf("undecided: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	fr.publishErr, fr.applied, fr.dropped = nil, 1858, 3
	rec = g.do("POST", reviewBase+"/publish", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/changes" {
		t.Fatalf("publish: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(events) != 1 || events[0].Action != "changeset.publish" {
		t.Errorf("audit = %+v", events)
	}
	// Choices posted with Publish are saved first, so a Drop that was never saved is not published.
	fr.decides, fr.publishes = nil, 0
	form := url.Values{"item_3": {"approved"}, "item_4": {"dropped"}, "group_redirects": {"dropped"}}
	if rec := g.do("POST", reviewBase+"/publish", "owner-token", form, nil); rec.Code != 303 || fr.publishes != 1 {
		t.Fatalf("publish with choices: %d, %d publishes", rec.Code, fr.publishes)
	}
	if want := []decideCall{{42, []int{3}, "approved"}, {42, []int{4, 10, 11}, "dropped"}}; !reflect.DeepEqual(fr.decides, want) {
		t.Errorf("decides before publish = %+v, want %+v", fr.decides, want)
	}
	fr.decides, fr.publishes = nil, 0
	if rec := g.do("POST", reviewBase+"/publish", "owner-token", url.Values{"item_4": {"nope"}}, nil); rec.Code != 400 || fr.publishes != 0 {
		t.Errorf("bad choice must not publish: %d, %d publishes", rec.Code, fr.publishes)
	}
	fr.publishErr = ErrReviewExpired
	if rec := g.do("POST", reviewBase+"/publish", "owner-token", nil, nil); rec.Code != 409 {
		t.Errorf("expired: %d", rec.Code)
	}

	if rec := g.do("POST", reviewBase+"/discard", "editor-token", nil, nil); rec.Code != 403 || fr.discards != 0 {
		t.Fatalf("editor discard: %d", rec.Code)
	}
	rec = g.do("POST", reviewBase+"/discard", "owner-token", nil, nil)
	if rec.Code != 303 || fr.discards != 1 || rec.Header().Get("Location") != "/admin/plugins/hello/runs" {
		t.Errorf("discard: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if events[len(events)-1].Action != "changeset.discard" {
		t.Errorf("audit = %+v", events)
	}
	if rec := g.do("POST", "/admin/plugins/other/changesets/42/publish", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("other plugin: %d", rec.Code)
	}
}

func TestReviewSuggestionRefusalsFlash(t *testing.T) {
	t.Parallel()

	for _, refusal := range []error{ErrReviewSuggestionDecided, ErrReviewNotApplicable} {
		g, fr, _ := newReviewRig(t)
		grantSudo(g)
		fr.suggestErr = refusal
		rec := g.do("POST", reviewBase+"/suggestions", "owner-token", url.Values{"suggestion_7": {"approved"}}, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != reviewBase || rec.Header().Get("Set-Cookie") == "" {
			t.Errorf("%v: %d %s, want 303 back to the review with a flash", refusal, rec.Code, rec.Header().Get("Location"))
		}
	}
}
