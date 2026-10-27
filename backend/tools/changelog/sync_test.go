package main

import (
	"reflect"
	"strings"
	"testing"
)

// syncGit is a repo with v0.25.0 (first), v0.26.0 and two PRs in v0.26.0.
func syncGit() gitRunner {
	return fakeGit(map[string]string{
		"tag --sort=v:refname":         "legacy-hybrid\nv0.25.0\nv0.26.0\n",
		"log -1 --format=%cI v0.26.0":  "2026-10-19T09:29:00+11:00\n",
		"log -1 --format=%cI v0.25.0":  "2026-10-19T01:04:00+11:00\n",
		logFormat + "v0.25.0..v0.26.0": "Merge pull request #144 from o/feat/entities-subnav\x1fentities-subnav: sidebar\n\x1e\nMerge pull request #147 from o/ops/edge-v0.25.0\x1frelease: v0.25.0 on edge\n\x1e\n",
	})
}

var v0260 = Opts{Version: "v0.26.0", Channel: "edge"}

func TestSyncCreate(t *testing.T) {
	f, c := newFakeAPI(t)
	res, err := Sync(c, syncGit(), v0260)
	if err != nil || res.Action != "create" {
		t.Fatalf("%v %v", res, err)
	}
	if n := len(f.entities); n != 1 {
		t.Fatalf("%d entities", n)
	}
	var a map[string]any
	for _, e := range f.entities {
		a = e.Attributes
		if e.Status != "published" {
			t.Error("not published")
		}
	}
	if a["version"] != "v0.26.0" || a["version_sort"] != 26000.0 || a["channel"] != "edge" || a["yanked"] != false ||
		a["released_at"] != "2026-10-19" || a["generated_hash"] == "" {
		t.Errorf("attrs = %v", a)
	}
	if got := notesFromAttrs(a); !reflect.DeepEqual(got.Features, []string{"entities-subnav: sidebar (#144)"}) || len(got.Chores) != 0 {
		t.Errorf("notes = %+v", got)
	}
}

func TestSyncIdempotent(t *testing.T) { // ISC-13
	f, c := newFakeAPI(t)
	Sync(c, syncGit(), v0260)
	before := len(f.writes())
	res, err := Sync(c, syncGit(), v0260)
	if err != nil || res.Action != "noop" {
		t.Fatalf("%v %v", res, err)
	}
	if len(f.writes()) != before || len(f.entities) != 1 {
		t.Error("second run wrote")
	}
}

func TestSyncMetaOnly(t *testing.T) { // ISC-14: channel moves, notes untouched
	f, c := newFakeAPI(t)
	Sync(c, syncGit(), v0260)
	res, err := Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "stable", Yanked: ptr(true)})
	if err != nil || res.Action != "update-meta" {
		t.Fatalf("%v %v", res, err)
	}
	for _, e := range f.entities {
		if e.Attributes["channel"] != "stable" || e.Attributes["yanked"] != true || len(e.Attributes["features"].([]any)) != 1 {
			t.Errorf("attrs = %v", e.Attributes)
		}
	}
}

func TestSyncRegeneratesUneditedNotes(t *testing.T) { // ISC-15
	f, c := newFakeAPI(t)
	Sync(c, syncGit(), v0260)
	g := fakeGit(map[string]string{
		"tag --sort=v:refname":         "v0.25.0\nv0.26.0\n",
		"log -1 --format=%cI v0.26.0":  "2026-10-19T09:29:00+11:00\n",
		logFormat + "v0.25.0..v0.26.0": "Merge pull request #144 from o/feat/entities-subnav\x1fentities-subnav: sidebar\n\x1e\nMerge pull request #150 from o/fix/late\x1fa late fix\n\x1e\n",
	})
	res, err := Sync(c, g, v0260)
	if err != nil || res.Action != "update-notes" {
		t.Fatalf("%v %v", res, err)
	}
	for _, e := range f.entities {
		if n := notesFromAttrs(e.Attributes); len(n.Fixes) != 1 || e.Attributes["generated_hash"] != n.Hash() {
			t.Errorf("attrs = %v", e.Attributes)
		}
	}
}

func TestSyncNeverOverwritesEditedNotes(t *testing.T) { // ISC-16, A4
	f, c := newFakeAPI(t)
	Sync(c, syncGit(), v0260)
	var e *Entity
	for _, e = range f.entities {
	}
	e.Attributes["features"] = []any{"hand written"} // hash now mismatches
	e.Attributes["extra"] = "kept"

	// Nothing else differs: skipped, no write.
	n := len(f.writes())
	res, err := Sync(c, syncGit(), v0260)
	if err != nil || res.Action != "skip-edited" || len(f.writes()) != n {
		t.Fatalf("%v %v", res, err)
	}
	// Metadata still moves, notes and extra attrs survive.
	res, err = Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "stable"})
	if err != nil || res.Action != "update-meta" {
		t.Fatalf("%v %v", res, err)
	}
	a := f.entities[e.ID].Attributes
	if a["channel"] != "stable" || a["extra"] != "kept" || !reflect.DeepEqual(a["features"], []any{"hand written"}) {
		t.Errorf("attrs = %v", a)
	}
}

func TestSyncSummaryOnlyAndFirstRelease(t *testing.T) {
	f, c := newFakeAPI(t)
	if _, err := Sync(c, syncGit(), Opts{Version: "v0.25.0", Channel: "stable"}); err == nil {
		t.Error("first release without --summary-only must fail")
	}
	res, err := Sync(c, syncGit(), Opts{Version: "v0.25.0", Channel: "stable", SummaryOnly: true})
	if err != nil || res.Action != "create" {
		t.Fatalf("%v %v", res, err)
	}
	for _, e := range f.entities {
		n := notesFromAttrs(e.Attributes)
		if n.Summary != firstReleaseSummary || len(n.Features) != 0 {
			t.Errorf("notes = %+v", n)
		}
	}
}

func TestSyncDryRunOnlyGets(t *testing.T) { // ISC-20
	f, c := newFakeAPI(t)
	o := v0260
	o.DryRun = true
	if res, err := Sync(c, syncGit(), o); err != nil || res.Action != "create" {
		t.Fatalf("%v %v", res, err)
	}
	if len(f.writes()) != 0 {
		t.Fatalf("dry-run create wrote: %+v", f.writes())
	}
	Sync(c, syncGit(), v0260) // real create: 3 writes
	changedNotes := fakeGit(map[string]string{
		"tag --sort=v:refname":         "v0.25.0\nv0.26.0\n",
		"log -1 --format=%cI v0.26.0":  "2026-10-19T09:29:00+11:00\n",
		logFormat + "v0.25.0..v0.26.0": "Merge pull request #150 from o/fix/late\x1fa late fix\n\x1e\n",
	})
	if res, err := Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "stable", DryRun: true}); err != nil || res.Action != "update-meta" {
		t.Fatalf("%v %v", res, err)
	}
	if res, err := Sync(c, changedNotes, Opts{Version: "v0.26.0", Channel: "edge", DryRun: true}); err != nil || res.Action != "update-notes" {
		t.Fatalf("%v %v", res, err)
	}
	if w := f.writes(); len(w) != 3 {
		t.Errorf("dry-run wrote: %+v", w)
	}
}

func TestSyncRejectsBadChannelAndVersion(t *testing.T) {
	_, c := newFakeAPI(t)
	if _, err := Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "beta"}); err == nil {
		t.Error("bad channel accepted")
	}
	if _, err := Sync(c, syncGit(), Opts{Version: "latest", Channel: "edge"}); err == nil {
		t.Error("bad version accepted")
	}
}

func TestSyncPublishesStrandedDraft(t *testing.T) { // POST ok, status PATCH failed
	f, c := newFakeAPI(t)
	f.failAt = "PATCH /api/v1/entities/e1/status"
	if _, err := Sync(c, syncGit(), v0260); err == nil {
		t.Fatal("create should fail when publishing fails")
	}
	if f.entities["e1"].Status != "draft" {
		t.Fatal("setup: want a stranded draft")
	}
	f.failAt = ""
	o := v0260
	o.DryRun = true
	if res, err := Sync(c, syncGit(), o); err != nil || res.Action != "publish" || f.entities["e1"].Status != "draft" {
		t.Fatalf("dry-run: %v %v status=%s", res, err, f.entities["e1"].Status)
	}
	res, err := Sync(c, syncGit(), v0260)
	if err != nil || res.Action != "publish" || f.entities["e1"].Status != "published" {
		t.Fatalf("%v %v status=%s", res, err, f.entities["e1"].Status)
	}
	if res, _ := Sync(c, syncGit(), v0260); res.Action != "noop" {
		t.Errorf("after publish: %v", res)
	}
}

func ptr[T any](v T) *T { return &v }

func onlyEntity(f *fakeAPI) *Entity {
	for _, e := range f.entities {
		return e
	}
	return nil
}

func TestSyncKeepsYankedAndChannelWhenNotGiven(t *testing.T) {
	f, c := newFakeAPI(t)
	Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "stable", Yanked: ptr(true)})
	// No yanked, no channel: nothing to change.
	if res, err := Sync(c, syncGit(), Opts{Version: "v0.26.0"}); err != nil || res.Action != "noop" {
		t.Fatalf("%v %v", res, err)
	}
	// A channel alone must not touch yanked.
	if res, err := Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "edge"}); err != nil || res.Action != "update-meta" {
		t.Fatalf("%v %v", res, err)
	}
	if a := onlyEntity(f).Attributes; a["yanked"] != true || a["channel"] != "edge" {
		t.Errorf("attrs = %v", a)
	}
	// Yanked alone must not touch the channel; unyank clears it.
	Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "stable"})
	Sync(c, syncGit(), Opts{Version: "v0.26.0", Yanked: ptr(false)})
	if a := onlyEntity(f).Attributes; a["yanked"] != false || a["channel"] != "stable" {
		t.Errorf("attrs = %v", a)
	}
}

func TestSyncCreateNeedsChannel(t *testing.T) {
	f, c := newFakeAPI(t)
	if _, err := Sync(c, syncGit(), Opts{Version: "v0.26.0"}); err == nil || len(f.entities) != 0 {
		t.Errorf("create without channel: err=%v entities=%d", err, len(f.entities))
	}
	if _, err := Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "edge", Yanked: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if onlyEntity(f).Attributes["yanked"] != true {
		t.Error("create ignored --yanked")
	}
}

func TestSyncReleasedAtIsADayAndKeepsHandCorrection(t *testing.T) {
	f, c := newFakeAPI(t)
	g := fakeGit(map[string]string{
		"tag --sort=v:refname":         "v0.25.0\nv0.26.0\n",
		"log -1 --format=%cI v0.26.0":  "2026-10-19T23:30:00-08:00\n", // 20 Oct in UTC
		logFormat + "v0.25.0..v0.26.0": "",
	})
	Sync(c, g, v0260)
	e := onlyEntity(f)
	if e.Attributes["released_at"] != "2026-10-19" {
		t.Fatalf("released_at = %v", e.Attributes["released_at"])
	}
	e.Attributes["released_at"] = "2026-10-20" // the owner corrects it
	if res, err := Sync(c, g, Opts{Version: "v0.26.0", Channel: "stable"}); err != nil || res.Action != "update-meta" {
		t.Fatalf("%v %v", res, err)
	}
	if got := e.Attributes["released_at"]; got != "2026-10-20" {
		t.Errorf("hand correction reverted to %v", got)
	}
	delete(e.Attributes, "released_at") // an empty date is filled again
	Sync(c, g, v0260)
	if got := e.Attributes["released_at"]; got != "2026-10-19" {
		t.Errorf("released_at = %v", got)
	}
}

// strand creates v0.26.0 on edge whose publish call fails, leaving a draft.
func strand(t *testing.T) (*fakeAPI, *Client) {
	t.Helper()
	f, c := newFakeAPI(t)
	f.failAt = "PATCH /api/v1/entities/e1/status"
	if _, err := Sync(c, syncGit(), v0260); err == nil {
		t.Fatal("create should fail when publishing fails")
	}
	f.failAt = ""
	return f, c
}

func TestSyncStrandedDraftAppliesTheRunsChannel(t *testing.T) {
	f, c := strand(t)
	o := Opts{Version: "v0.26.0", Channel: "stable"}
	dry := o
	dry.DryRun = true
	before := len(f.writes())
	if res, err := Sync(c, syncGit(), dry); err != nil || res.Action != "publish+update-meta" || len(f.writes()) != before {
		t.Fatalf("dry-run: %v %v writes=%d", res, err, len(f.writes())-before)
	}
	res, err := Sync(c, syncGit(), o)
	e := f.entities["e1"]
	if err != nil || res.Action != "publish+update-meta" || e.Status != "published" ||
		e.Attributes["channel"] != "stable" || e.Attributes[pendingPublish] != false {
		t.Fatalf("%v %v status=%s attrs=%v", res, err, e.Status, e.Attributes)
	}
	if res, _ := Sync(c, syncGit(), o); res.Action != "noop" {
		t.Errorf("after publish: %v", res)
	}
}

func TestSyncLeavesAnOwnersStatusAlone(t *testing.T) {
	for _, status := range []string{"archived", "scheduled", "draft"} {
		f, c := newFakeAPI(t)
		Sync(c, syncGit(), v0260)
		e := f.entities["e1"]
		e.Status = status // the owner hid it; pending_publish was cleared by the create
		res, err := Sync(c, syncGit(), Opts{Version: "v0.26.0", Yanked: ptr(true)})
		if err != nil || res.Action != "update-meta" || res.Note == "" || e.Status != status || e.Attributes["yanked"] != true {
			t.Fatalf("%s: %v %v status=%s yanked=%v", status, res, err, e.Status, e.Attributes["yanked"])
		}
		for _, w := range f.writes() {
			if strings.HasSuffix(w.Path, "/status") && w.Method == "PATCH" && len(f.writes()) > 3 && w.Body["status"] != "published" {
				t.Errorf("%s: unexpected status write %+v", status, w)
			}
		}
		if res, _ := Sync(c, syncGit(), Opts{Version: "v0.26.0"}); res.Action != "noop" || e.Status != status {
			t.Errorf("%s: rerun %v status=%s", status, res, e.Status)
		}
	}
}

func TestSyncDoesNotPublishADraftWithoutPendingFlag(t *testing.T) {
	f, c := strand(t)
	delete(f.entities["e1"].Attributes, pendingPublish) // e.g. the owner unpublished it after it was published
	res, err := Sync(c, syncGit(), v0260)
	if err != nil || f.entities["e1"].Status != "draft" || res.Note == "" {
		t.Fatalf("%v %v status=%s", res, err, f.entities["e1"].Status)
	}
}

func TestPendingPublishIsNotPartOfTheNotesHash(t *testing.T) {
	_, c := newFakeAPI(t)
	Sync(c, syncGit(), v0260)
	e, _ := c.Find("v0.26.0")
	withFlag := notesFromAttrs(map[string]any{"summary": "s", pendingPublish: true}).Hash()
	if withFlag != notesFromAttrs(map[string]any{"summary": "s"}).Hash() || e.Attributes["generated_hash"] == "" {
		t.Error("pending_publish changed the hash")
	}
}
