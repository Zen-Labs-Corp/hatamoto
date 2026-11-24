package admin

import (
	"strings"
	"testing"
)

func cpolRig(builtin bool) *fakeComments {
	return &fakeComments{
		settings: CommentSettings{Enabled: true, Sort: "newest"},
		supports: map[string]bool{"schema-policy": true},
		types: []SchemaCommentConfig{
			{ID: "11111111-1111-1111-1111-111111111111", Slug: "notes", Name: "Notes", Mode: "", Order: "oldest",
				Policy: CommentPolicy{Enabled: true, MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}},
			{ID: "22222222-2222-2222-2222-222222222222", Slug: "articles", Name: "Articles", Mode: "closed",
				Policy: CommentPolicy{MaxDepth: 10, EditWindowMinutes: 15}},
			{ID: "33333333-3333-3333-3333-333333333333", Slug: "posts", Name: "Posts", Builtin: builtin,
				Policy: CommentPolicy{MaxDepth: 10, EditWindowMinutes: 15}},
		},
	}
}

// cpolCard returns the page text from the card of a type to the next card.
func cpolCard(t *testing.T, body, slug string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, `id="comments-type-`+slug+`"`)
	if !ok {
		t.Fatalf("no card for %s", slug)
	}
	card, _, _ := strings.Cut(rest, "</form>")
	return card
}

func TestCommentPolicyOwnerCards(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cpolRig(false))
	body := csetGet(t, p, "owner-token")
	on, off := cpolCard(t, body, "notes"), cpolCard(t, body, "articles")
	for _, want := range []string{cpolOnWarning, `name="max_depth"`, `value="32"`, `name="edit_window_minutes"`, `value="120"`, `name="slow_mode_seconds"`, `value="30"`, "Comments: on", ">Save<"} {
		if !strings.Contains(on, want) {
			t.Errorf("on card lacks %q", want)
		}
	}
	for _, want := range []string{cpolOffHelp, "Comments: closed", `data-hm-open="comments-policy-articles"`} {
		if !strings.Contains(off, want) {
			t.Errorf("off card lacks %q", want)
		}
	}
	for _, bad := range []string{`name="max_depth"`, `name="edit_window_minutes"`, `name="slow_mode_seconds"`, cpolOnWarning} {
		if strings.Contains(off, bad) {
			t.Errorf("off card has %q", bad)
		}
	}
	if !strings.Contains(on, `value="oldest" selected`) {
		t.Errorf("order not selected on the on card: %s", on)
	}
	for _, want := range []string{`id="comments-policy-articles"`, cpolConfirm, cpolTick, `data-hm-ack-submit`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, `id="comments-policy-notes"`) {
		t.Error("dialog for a type whose policy is already on")
	}
}

func TestCommentPolicyEditorReadOnly(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cpolRig(false))
	body := csetGet(t, p, "editor-token")
	for _, slug := range []string{"notes", "articles"} {
		if card := cpolCard(t, body, slug); !strings.Contains(card, "<fieldset class=\"hm-field__group\" disabled") {
			t.Errorf("%s: fieldset not disabled", slug)
		}
	}
	for _, bad := range []string{`id="comments-policy-articles"`, `data-hm-open="comments-policy-`, ">Save<"} {
		if strings.Contains(body, bad) {
			t.Errorf("editor page has %q", bad)
		}
	}
}

func TestCommentPolicyBuiltinReadOnly(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cpolRig(true))
	body := csetGet(t, p, "owner-token")
	card := cpolCard(t, body, "posts")
	for _, want := range []string{"cannot use the community policy yet", "<fieldset class=\"hm-field__group\" disabled"} {
		if !strings.Contains(card, want) {
			t.Errorf("built-in card lacks %q", want)
		}
	}
	if strings.Contains(card, ">Save<") || strings.Contains(body, `id="comments-policy-posts"`) {
		t.Error("built-in card is writable")
	}
}

func TestCommentPolicyHiddenWhileCommentsOff(t *testing.T) {
	t.Parallel()

	f := cpolRig(false)
	f.settings.Enabled = false
	p := newCommentsRig(t, f)
	if body := csetGet(t, p, "owner-token"); strings.Contains(body, "Community policy") {
		t.Error("panel shown while comments are off")
	}
}
