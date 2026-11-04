package admin

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/changeset"
)

func historyRows() []HistoryRow {
	at := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	return []HistoryRow{
		{ID: 1, Plugin: "hello", RunName: "WordPress Import", State: changeset.StatePublished, By: "Olga", When: at, Applied: 1858},
		{ID: 2, Plugin: "hello", RunName: "Staged one", State: changeset.StateStaged, By: "Olga", When: at},
		{ID: 3, Plugin: "hello", RunName: "Old", State: changeset.StateExpired, When: at},
		{ID: 4, Plugin: "hello", RunName: "Thrown", State: changeset.StateDiscarded, When: at},
		{ID: 5, Plugin: "hello", RunName: "Undone", State: changeset.StateReverted, When: at, Applied: 5},
	}
}

func TestChangesPage(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	fr.history = historyRows()
	fr.preview = UndoPreview{Applied: 1858, EditedSince: 4, Settings: 2}
	rec := g.do("GET", "/admin/settings/changes", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("changes: %d", rec.Code)
	}
	for _, want := range []string{
		"Published", "Waiting for you", "Expired, deleted", "Discarded", "Reverted", "1,858",
		`href="/admin/plugins/hello/changesets/2"`, "Undo whole run", `data-hm-open="undo-1"`,
		"Undo run #1?", "Removes the 1,858 changes this import published and restores the 2 site-wide settings.",
		"4 imported pages were edited by you since then. They are now yours and stay as they are.",
		`action="/admin/settings/changes/1/undo"`, "Undo run",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("changes page lacks %q", want)
		}
	}
	if strings.Count(body, "Undo whole run</span>") != 1 || strings.Count(body, `<dialog `) != 2 {
		t.Error("only the published row offers undo")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	fr.preview.Settings = 1
	if b := g.do("GET", "/admin/settings/changes", "owner-token", nil, nil).Body.String(); !strings.Contains(b, "and restores the 1 site-wide setting.") {
		t.Error("one setting is singular")
	}
	fr.preview.Settings = 0
	if b := g.do("GET", "/admin/settings/changes", "owner-token", nil, nil).Body.String(); strings.Contains(b, "site-wide") || !strings.Contains(b, "this import published.") {
		t.Error("no mention of settings when none were changed")
	}
	fr.preview.EditedSince = 0
	if strings.Contains(g.do("GET", "/admin/settings/changes", "owner-token", nil, nil).Body.String(), "edited by you since then") {
		t.Error("no callout when nothing was edited")
	}
	if rec := g.do("GET", "/admin/settings/changes", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
	if rec := newRig(t).do("GET", "/admin/settings/changes", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("not wired: %d", rec.Code)
	}
}

func TestChangesPageRunningRunHasNoReview(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	fr.history = []HistoryRow{{ID: 9, Plugin: "hello", RunName: "Half done", State: changeset.StateStaged, Building: true, When: time.Now()}}
	body := g.do("GET", "/admin/settings/changes", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Running") || strings.Contains(body, "Waiting for you") || strings.Contains(body, "/changesets/9") || strings.Contains(body, ">Review<") {
		t.Error("a building run is labelled Running with no Review path")
	}
}

func TestChangesPaging(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	for i := 1; i <= 30; i++ {
		fr.history = append(fr.history, HistoryRow{ID: int64(i), Plugin: "hello", RunName: "Run", State: changeset.StateDiscarded, When: time.Now()})
	}
	body := g.do("GET", "/admin/settings/changes", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "hm-pagination") || !strings.Contains(body, "page=2") {
		t.Error("30 rows page at 25")
	}
}

func TestChangesUndo(t *testing.T) {
	t.Parallel()

	g, fr, _ := newReviewRig(t)
	fr.restored, fr.kept = 1854, 4
	if rec := g.do("POST", "/admin/settings/changes/1/undo", "editor-token", url.Values{}, nil); rec.Code != 403 || fr.undos != 0 {
		t.Fatalf("editor: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/settings/changes/1/undo", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), g.a.sudoPath()) || fr.undos != 0 {
		t.Fatalf("no sudo: %d %s %d", rec.Code, rec.Header().Get("Location"), fr.undos)
	}
	grantSudo(g)
	rec = g.do("POST", "/admin/settings/changes/1/undo", "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/changes" || fr.undos != 1 {
		t.Fatalf("undo: %d %s %d", rec.Code, rec.Header().Get("Location"), fr.undos)
	}
	fr.undoErr = ErrReviewNotPublished
	if rec := g.do("POST", "/admin/settings/changes/1/undo", "owner-token", url.Values{}, nil); rec.Code != 409 {
		t.Errorf("not published: %d", rec.Code)
	}
}
