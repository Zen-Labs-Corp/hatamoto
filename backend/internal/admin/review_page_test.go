package admin

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/changeset"
)

type decideCall struct {
	ID       int64
	Seqs     []int
	Decision string
}

type suggestCall struct {
	ID, Proposal int64
	Decision     string
}

// fakeReviewer is a ChangesetReviewer that answers from fields and records its calls.
type fakeReviewer struct {
	mu   sync.Mutex
	data ReviewData
	err  error // answered by every method when set

	decides  []decideCall
	suggests []suggestCall
	// suggestErr, publishErr, discardErr, undoErr, undoPreviewErr override err for one method.
	suggestErr, publishErr, discardErr, undoErr error
	publishes, discards, undos                  int
	applied, dropped                            int
	history                                     []HistoryRow
	preview                                     UndoPreview
	restored, kept                              int
}

func (f *fakeReviewer) Review(context.Context, int64) (ReviewData, error) { return f.data, f.err }

func (f *fakeReviewer) Decide(_ context.Context, id int64, seqs []int, decision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decides = append(f.decides, decideCall{id, seqs, decision})
	return f.err
}

func (f *fakeReviewer) DecideSuggestion(_ context.Context, id, proposal int64, decision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suggests = append(f.suggests, suggestCall{id, proposal, decision})
	if f.suggestErr != nil {
		return f.suggestErr
	}
	return f.err
}

func (f *fakeReviewer) Publish(context.Context, int64) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishes++
	if f.publishErr != nil {
		return 0, 0, f.publishErr
	}
	return f.applied, f.dropped, f.err
}

func (f *fakeReviewer) Discard(context.Context, int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discards++
	if f.discardErr != nil {
		return f.discardErr
	}
	return f.err
}

func (f *fakeReviewer) History(_ context.Context, limit, offset int) ([]HistoryRow, error) {
	if offset >= len(f.history) {
		return nil, f.err
	}
	return f.history[offset:min(len(f.history), offset+limit)], f.err
}

func (f *fakeReviewer) UndoPreview(context.Context, int64) (UndoPreview, error) {
	return f.preview, f.err
}

func (f *fakeReviewer) Undo(context.Context, int64) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undos++
	if f.undoErr != nil {
		return 0, 0, f.undoErr
	}
	return f.restored, f.kept, f.err
}

// reviewData is a staged changeset with two suggestions, four risky items and a redirect group.
func reviewData() ReviewData {
	return ReviewData{
		ID: 42, Plugin: "hello", RunName: "WordPress Import", State: changeset.StateStaged, ExpiresAt: time.Now().Add(71*time.Hour - time.Minute),
		Items: 1861, Low: 1840, High: 5, Critical: 0, HighDecided: 1,
		Suggestions: []Suggestion{
			{ID: 7, Kind: "site_settings", Title: "Site title", Applicable: true, Fields: []SuggestionField{{Name: "Site title", Old: "Old name", New: "New name"}}},
			{ID: 8, Kind: "robots", Title: "Robots rules", Applicable: false, Fields: []SuggestionField{{Name: "Rules", Old: "", New: "Disallow: /"}}},
		},
		Risky: []ReviewItem{
			{Seq: 3, Kind: "entity", Op: "upsert", Label: "About us", Risk: "high", Reason: "link_query", Old: "?a=1", New: "?a=2"},
			{Seq: 4, Kind: "entity", Op: "upsert", Label: "Contact", Risk: "high", Reason: "hidden_char", Decision: "approved"},
		},
		RedirectGroup: &RedirectGroup{Count: 212, Seqs: []int{10, 11}},
		Kinds:         []KindRow{{Label: "Pages", New: 1200, Updated: 3}, {Label: "Media", New: 600}},
		Skipped:       []SkippedRow{{Label: "Pricing", Why: "You edited it after the import started"}, {Label: "Team", Why: "You edited it after the import started"}},
		PublishCount:  1858, Previewable: true,
	}
}

func newReviewRig(t *testing.T) (*rig, *fakeReviewer, *fakeChangesets) {
	t.Helper()
	g, cs, _ := newChangesetRig(t, "")
	fr := &fakeReviewer{data: reviewData()}
	g.a.deps.Growth.Reviewer = fr
	return g, fr, cs
}

func TestReviewPage(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	rec := g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("review: %d", rec.Code)
	}
	for _, want := range []string{
		"Changeset #42: WordPress Import", "Waiting for you", "1,861 changes", "2 suggestions", "5 risky",
		"2 skipped (you edited them)", "Deleted in 71 h if not decided", "Open preview",
		`action="/admin/plugins/hello/changesets/42/suggestions"`, `name="suggestion_7"`, `name="suggestion_8"`,
		`<code class="hm-valuediff"><del>Old name</del> <ins>New name</ins></code>`, "Each approval asks you to sign in again.", "Save suggestion decisions",
		`action="/admin/plugins/hello/changesets/42/decide"`, `name="item_3"`, `name="item_4"`, `name="group_redirects"`, "212 redirects", "All to pages in this changeset",
		"1 of 5 decided", "Remove it", "hidden character", "Ordinary changes", "Pages", "1,200", "Skipped", "Pricing", "hm-table--wrap",
		`class="hm-savebar"`, "4 risky items and 2 suggestions still need a decision.", "Publish 1,858 changes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("review page lacks %q", want)
		}
	}
	// The page is one submit per form: the suggestions form never nests inside the decisions form.
	if strings.Count(body, "<form method=\"post\"") < 1 || strings.Index(body, `id="review-form"`) < strings.Index(body, `/changesets/42/suggestions"`) {
		t.Error("suggestions form comes first and is not nested in the decisions form")
	}
	// Discard comes before Publish in the savebar, and Publish is disabled while undecided.
	bar := body[strings.Index(body, `class="hm-savebar"`):]
	if d, p := strings.Index(bar, "Discard all"), strings.Index(bar, "Publish 1,858 changes"); d < 0 || p < d {
		t.Errorf("savebar order: Discard %d, Publish %d", d, p)
	}
	publish := regexp.MustCompile(`<button[^>]*formaction="[^"]*/publish"[^>]*>`).FindString(bar)
	if !strings.Contains(publish, " disabled") {
		t.Errorf("Publish must be disabled while decisions are missing: %s", publish)
	}
	// A saved decision is the pressed (checked) one.
	if !regexp.MustCompile(`name="item_4" value="approved" checked`).MatchString(body) || regexp.MustCompile(`name="item_3"[^>]* checked`).MatchString(body) {
		t.Error("only the stored decision is checked")
	}
	// The non-applicable suggestion offers only Reject, and says why.
	row := body[strings.Index(body, `name="suggestion_8"`)-300:]
	row = row[:strings.Index(row, "</tr>")]
	if strings.Contains(row, `value="approved"`) || !strings.Contains(row, `value="rejected"`) || !strings.Contains(row, "Cannot be applied in this release") {
		t.Errorf("non-applicable suggestion row: %s", row)
	}
	if !strings.Contains(body[strings.Index(body, `name="suggestion_7"`)-100:], `name="suggestion_7" value="approved"`) {
		t.Error("an applicable suggestion offers Approve")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}

	// Every risky item and suggestion decided: Publish is enabled.
	fr.data.CanPublish, fr.data.HighDecided = true, 5
	fr.data.Suggestions = nil
	body = g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil).Body.String()
	publish = regexp.MustCompile(`<button[^>]*formaction="[^"]*/publish"[^>]*>`).FindString(body)
	if publish == "" || strings.Contains(publish, "disabled") {
		t.Errorf("Publish must be enabled once decided: %q", publish)
	}
	if strings.Contains(body, "suggestions\"") && strings.Contains(body, "Site-wide suggestions") {
		t.Error("no suggestions, no suggestions card")
	}
}

func TestReviewPageClosedStates(t *testing.T) {
	t.Parallel()

	g, fr, cs := newReviewRig(t)
	for state, want := range map[string]string{
		changeset.StatePublished: "These changes are live.",
		changeset.StateDiscarded: "thrown away",
		changeset.StateReverted:  "undone",
	} {
		cs.h.State = state
		body := g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, want) || !strings.Contains(body, `href="/admin/settings/changes"`) || strings.Contains(body, "<form method=\"post\" action=\"/admin/plugins/hello/changesets") {
			t.Errorf("%s: want a read-only summary with %q", state, want)
		}
	}
	cs.h.State, cs.h.ExpiresAt = changeset.StateStaged, time.Now().Add(-time.Minute)
	body := g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "expired") || strings.Contains(body, "review-form") {
		t.Error("an expired changeset shows the notice and no form")
	}
	fr.err = ErrReviewNotFound
	cs.h.ExpiresAt = time.Now().Add(time.Hour)
	if rec := g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("reviewer says not found: %d", rec.Code)
	}
}

// Without a reviewer the page keeps the plain item list, and nothing can be decided from it.
func TestReviewPageFallsBackWithoutReviewer(t *testing.T) {
	t.Parallel()

	g, _, _ := newChangesetRig(t, "")
	body := g.do("GET", "/admin/plugins/hello/changesets/42", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Changes in this changeset") || strings.Contains(body, "hm-savebar") {
		t.Error("fallback is the plain list")
	}
}
