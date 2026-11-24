package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// The confirm page is a template of its own; the a11y test must cover it.
func init() {
	areaPages["commentspolicy"] = []string{"settings/comments-policy"}
}

const (
	cpolNotesURL    = "/types/11111111-1111-1111-1111-111111111111"
	cpolArticlesURL = "/types/22222222-2222-2222-2222-222222222222"
	cpolPostsURL    = "/types/33333333-3333-3333-3333-333333333333"
)

func cpolTypeByID(f *fakeComments, id string) SchemaCommentConfig {
	for _, c := range f.types {
		if c.ID == id {
			return c
		}
	}
	return SchemaCommentConfig{}
}

func TestCommentPolicyTurnOnNeedsTick(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	p := newCommentsRig(t, f)
	form := url.Values{"policy": {"true"}, "order": {""}}
	rec := csetPost(p, cpolArticlesURL, "owner-token", form, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), cpolConfirm) || !strings.Contains(rec.Body.String(), cpolTick) {
		t.Fatalf("POST without tick = %d", rec.Code)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if len(f.calls) != 0 || len(p.events) != 0 {
		t.Errorf("wrote without the tick: %v %v", f.calls, p.events)
	}

	form.Set("understand", "1")
	rec = csetPost(p, cpolArticlesURL, "owner-token", form, nil)
	if rec.Code != 303 {
		t.Fatalf("POST with tick = %d", rec.Code)
	}
	if got := cpolTypeByID(f, "22222222-2222-2222-2222-222222222222").Policy; got != (CommentPolicy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}) {
		t.Errorf("stored policy = %+v", got)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.policy" {
		t.Fatalf("audit = %+v", p.events)
	}
	d := p.events[0].Detail
	if d["old"] != false || d["new"] != true || d["type"] != "articles" {
		t.Errorf("audit detail = %+v", d)
	}
	if ch, _ := d["changed"].([]string); strings.Join(ch, ",") != "policy" {
		t.Errorf("changed = %v", d["changed"])
	}
}

func TestCommentPolicySaveNumbersAndOff(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	p := newCommentsRig(t, f)
	form := url.Values{"policy": {"true"}, "order": {"newest"}, "max_depth": {"20"}, "edit_window_minutes": {"60"}, "slow_mode_seconds": {"5"}}
	if rec := csetPost(p, cpolNotesURL, "owner-token", form, nil); rec.Code != 303 {
		t.Fatalf("save = %d", rec.Code)
	}
	got := cpolTypeByID(f, "11111111-1111-1111-1111-111111111111")
	if got.Order != "newest" || got.Policy != (CommentPolicy{Enabled: true, MaxDepth: 20, EditWindowMinutes: 60, SlowModeSeconds: 5}) {
		t.Errorf("stored = %+v", got)
	}
	// Off needs no tick and keeps the numbers.
	if rec := csetPost(p, cpolNotesURL, "owner-token", url.Values{"policy": {"off"}}, nil); rec.Code != 303 {
		t.Fatalf("off = %d", rec.Code)
	}
	got = cpolTypeByID(f, "11111111-1111-1111-1111-111111111111")
	if got.Policy != (CommentPolicy{MaxDepth: 20, EditWindowMinutes: 60, SlowModeSeconds: 5}) || got.Order != "newest" {
		t.Errorf("after off = %+v", got)
	}
	ev := p.events[len(p.events)-1]
	if ch, _ := ev.Detail["changed"].([]string); strings.Join(ch, ",") != "policy" {
		t.Errorf("off changed = %v", ev.Detail["changed"])
	}
}

func TestCommentPolicyBadNumber(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	p := newCommentsRig(t, f)
	form := url.Values{"policy": {"true"}, "max_depth": {"33"}, "edit_window_minutes": {"60"}}
	rec := csetPost(p, cpolNotesURL, "owner-token", form, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Enter a whole number from 1 to 32.") || !strings.Contains(body, `value="33"`) {
		t.Fatalf("max_depth 33 = %d", rec.Code)
	}
	if len(f.calls) != 0 || len(p.events) != 0 {
		t.Errorf("wrote a refused value: %v %v", f.calls, p.events)
	}
}

func TestCommentPolicyOrderRank(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	p := newCommentsRig(t, f)
	if rec := csetPost(p, cpolNotesURL, "owner-token", url.Values{"order": {"rank"}}, nil); rec.Code != 422 {
		t.Errorf("rank without a stored rank = %d", rec.Code)
	}
	f.types[0].Order = "rank"
	if rec := csetPost(p, cpolNotesURL, "owner-token", url.Values{"order": {"rank"}}, nil); rec.Code != 303 {
		t.Errorf("stored rank kept = %d", rec.Code)
	}
}

func TestCommentPolicyEditorForbidden(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	p := newCommentsRig(t, f)
	if rec := csetPost(p, cpolNotesURL, "editor-token", url.Values{"policy": {"off"}}, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if len(f.calls) != 0 || len(p.events) != 0 {
		t.Errorf("editor wrote: %v %v", f.calls, p.events)
	}
}

func TestCommentPolicyBuiltinAndUnknown(t *testing.T) {
	t.Parallel()

	f := cpolRig(true)
	p := newCommentsRig(t, f)
	rec := csetPost(p, cpolPostsURL, "owner-token", url.Values{"policy": {"true"}, "understand": {"1"}}, nil)
	if rec.Code != 303 || len(f.calls) != 0 || len(p.events) != 0 {
		t.Errorf("built-in = %d, calls %v", rec.Code, f.calls)
	}
	if rec := csetPost(p, "/types/99999999-9999-9999-9999-999999999999", "owner-token", url.Values{"policy": {"off"}}, nil); rec.Code != 404 {
		t.Errorf("unknown type = %d", rec.Code)
	}
}

func TestCommentPolicyBadNumberInErrorSummary(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cpolRig(false))
	rec := csetPost(p, cpolNotesURL, "owner-token", url.Values{"max_depth": {"33"}}, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, `href="#comments-type-notes-max-depth"`) {
		t.Errorf("no summary link to the refused field (%d)", rec.Code)
	}
}
