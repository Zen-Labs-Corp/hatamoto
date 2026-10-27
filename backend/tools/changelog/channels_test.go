package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeReleases serves /channels/<ch>.json; a missing channel is a 404. It
// records whether any request carried an API key.
func fakeReleases(t *testing.T, files map[string]string) (string, *bool) {
	t.Helper()
	sawKey := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "" {
			sawKey = true
		}
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/channels/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &sawKey
}

var testNow = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

// liveFiles is the 2026-10-20 picture: v0.26.0 scheduled for stable tomorrow,
// v0.27.1 on canary with edge in two days, v0.25.0 yanked on edge.
var liveFiles = map[string]string{
	"canary.json": `{"index":{"channel":"canary","releases":[{"version":"v0.27.1","ramp":[{"from":"2026-10-20T00:45:00Z","pct":100}]}],"yanked":[]},"sig":"x"}`,
	"edge.json": `{"index":{"channel":"edge","releases":[
		{"version":"v0.25.0","ramp":[{"from":"2026-10-18T14:13:00Z","pct":100}]},
		{"version":"v0.26.0","ramp":[{"from":"2026-10-19T01:28:00Z","pct":100}]},
		{"version":"v0.27.1","ramp":[{"from":"2026-10-22T00:45:00Z","pct":100}]}],
		"yanked":[{"version":"v0.25.0","reason":"bad"}]}}`,
	"stable.json": `{"channel":"stable","releases":[{"version":"v0.26.0","ramp":[{"from":"2026-10-21T04:29:00Z","pct":5}]}],"yanked":[]}`,
}

func TestLiveChannelsUsesStartedRampsOnly(t *testing.T) {
	base, sawKey := fakeReleases(t, liveFiles)
	got, err := LiveChannels(nil, base, testNow)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Live{
		"v0.25.0": {Channel: "edge", Yanked: true},
		"v0.26.0": {Channel: "edge"},   // stable starts tomorrow
		"v0.27.1": {Channel: "canary"}, // edge starts in two days
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for v, w := range want {
		if got[v] != w {
			t.Errorf("%s = %+v, want %+v", v, got[v], w)
		}
	}
	if *sawKey {
		t.Error("an API key was sent to the public manifests")
	}
	// A day later v0.26.0 has reached stable.
	got, _ = LiveChannels(nil, base, testNow.Add(24*time.Hour))
	if got["v0.26.0"].Channel != "stable" {
		t.Errorf("after the ramp start: %+v", got["v0.26.0"])
	}
}

func TestLiveChannelsMissingChannelAndErrors(t *testing.T) {
	base, _ := fakeReleases(t, map[string]string{"edge.json": liveFiles["edge.json"]})
	got, err := LiveChannels(nil, base, testNow)
	if err != nil || got["v0.26.0"].Channel != "edge" {
		t.Fatalf("missing canary/stable must be empty, got %+v, %v", got, err)
	}
	bad, _ := fakeReleases(t, map[string]string{"canary.json": `{"releases":[{"version":"v1.0.0","ramp":[{"from":"soon"}]}]}`})
	if _, err := LiveChannels(nil, bad, testNow); err == nil {
		t.Error("bad ramp time accepted")
	}
	broken, _ := fakeReleases(t, map[string]string{"canary.json": `{`})
	if _, err := LiveChannels(nil, broken, testNow); err == nil {
		t.Error("broken JSON accepted")
	}
}

func TestSyncAcceptsCanary(t *testing.T) {
	f, c := newFakeAPI(t)
	if _, err := Sync(c, syncGit(), Opts{Version: "v0.26.0", Channel: "canary"}); err != nil {
		t.Fatal(err)
	}
	if onlyEntity(f).Attributes["channel"] != "canary" {
		t.Error("canary not stored")
	}
}

func refreshEnv(api, releases string) env {
	e := credEnv(api, syncGit())
	e.Getenv = mapEnv(map[string]string{envURL: api, envKey: testKey, envSchema: "schema-1", "HATAMOTO_RELEASES_URL": releases})
	e.Now = func() time.Time { return testNow }
	return e
}

func TestRefreshFollowsTheManifests(t *testing.T) {
	f, c := newFakeAPI(t)
	base, _ := fakeReleases(t, liveFiles)
	e := refreshEnv(c.Base, base)
	code, out, errOut := runCLI(t, e, "refresh")
	if code != 0 || out != "v0.25.0 create [edge, yanked]\nv0.26.0 create [edge]\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, _ = runCLI(t, e, "refresh"); out != "v0.25.0 noop [edge, yanked]\nv0.26.0 noop [edge]\n" {
		t.Errorf("rerun: code=%d out=%q", code, out)
	}
	// Tomorrow v0.26.0 reaches stable; an edited entry still gets the badge.
	for _, ent := range f.entities {
		if ent.Attributes["version"] == "v0.26.0" {
			ent.Attributes["features"] = []any{"hand written"}
		}
	}
	e.Now = func() time.Time { return testNow.Add(24 * time.Hour) }
	if code, out, _ = runCLI(t, e, "refresh"); !strings.Contains(out, "v0.26.0 update-meta [stable]") {
		t.Errorf("promotion: code=%d out=%q", code, out)
	}
	for _, ent := range f.entities {
		if ent.Attributes["version"] == "v0.26.0" {
			if ent.Attributes["channel"] != "stable" || ent.Attributes["features"].([]any)[0] != "hand written" {
				t.Errorf("v0.26.0 = %+v", ent.Attributes)
			}
		}
	}
	n := len(f.writes())
	e.Now = func() time.Time { return testNow.Add(48 * time.Hour) }
	if code, _, _ = runCLI(t, e, "refresh", "--dry-run"); code != 0 || len(f.writes()) != n {
		t.Error("dry-run wrote")
	}
}

func TestRefreshSkipsVersionsNotLiveYet(t *testing.T) {
	_, c := newFakeAPI(t)
	base, _ := fakeReleases(t, map[string]string{"canary.json": liveFiles["canary.json"]})
	code, out, _ := runCLI(t, refreshEnv(c.Base, base), "refresh")
	if code != 0 || out != "v0.25.0 skip (not live on any channel, or halted)\nv0.26.0 skip (not live on any channel, or halted)\n" {
		t.Errorf("code=%d out=%q", code, out)
	}
}

func TestSyncChannelAuto(t *testing.T) {
	f, c := newFakeAPI(t)
	base, _ := fakeReleases(t, liveFiles)
	e := refreshEnv(c.Base, base)
	if code, out, errOut := runCLI(t, e, "sync", "v0.26.0", "--channel", "auto"); code != 0 || out != "v0.26.0 create\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if a := onlyEntity(f).Attributes; a["channel"] != "edge" || a["yanked"] != false {
		t.Errorf("attrs %+v", a)
	}
	// An explicit --unyank wins over the manifest.
	if code, _, _ := runCLI(t, e, "sync", "v0.25.0", "--channel", "auto", "--summary-only", "--unyank"); code != 0 {
		t.Fatal("v0.25.0 sync failed")
	}
	for _, ent := range f.entities {
		if ent.Attributes["version"] == "v0.25.0" && ent.Attributes["yanked"] != false {
			t.Error("--unyank ignored under auto")
		}
	}
	if code, _, errOut := runCLI(t, e, "sync", "v9.9.9", "--channel", "auto"); code != 1 || !strings.Contains(errOut, "not live") {
		t.Errorf("unknown version: code=%d err=%q", code, errOut)
	}
}

func TestLiveChannelsIgnoresHaltedEntries(t *testing.T) {
	// v0.28.0 was scheduled, then halted on edge and stable: the badge stays canary
	// even after the edge and stable ramps start.
	base, _ := fakeReleases(t, map[string]string{
		"canary.json": `{"index":{"releases":[{"version":"v0.28.0","ramp":[{"from":"2026-10-20T08:29:00Z","pct":100}]}]}}`,
		"edge.json":   `{"index":{"releases":[{"version":"v0.28.0","halted":true,"ramp":[{"from":"2026-10-20T08:30:00Z","pct":100}]}]}}`,
		"stable.json": `{"index":{"releases":[{"version":"v0.28.0","halted":true,"ramp":[{"from":"2026-10-20T08:31:00Z","pct":5}]}]}}`,
	})
	got, err := LiveChannels(nil, base, testNow)
	if err != nil || got["v0.28.0"].Channel != "canary" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// Halted everywhere: not live at all.
	all, _ := fakeReleases(t, map[string]string{
		"edge.json": `{"index":{"releases":[{"version":"v0.28.0","halted":true,"ramp":[{"from":"2026-10-20T08:30:00Z","pct":100}]}]}}`,
	})
	if got, _ := LiveChannels(nil, all, testNow); got["v0.28.0"].Channel != "" {
		t.Errorf("halted everywhere = %+v", got["v0.28.0"])
	}
}

func TestLiveChannelsNoManifestAtAllIsAnError(t *testing.T) {
	base, _ := fakeReleases(t, map[string]string{})
	if _, err := LiveChannels(nil, base, testNow); err == nil || !strings.Contains(err.Error(), "no channel manifest") {
		t.Errorf("err = %v", err)
	}
}

func TestRefreshKeepsGoingPastAFailingVersion(t *testing.T) {
	f, c := newFakeAPI(t)
	base, _ := fakeReleases(t, liveFiles)
	// Two entries for v0.25.0: Find refuses, v0.26.0 must still be created.
	f.seed("v0.25.0", map[string]any{"version": "v0.25.0"})
	f.seed("v0.25.0", map[string]any{"version": "v0.25.0"})
	code, out, errOut := runCLI(t, refreshEnv(c.Base, base), "refresh")
	if code != 1 || !strings.Contains(out, "v0.26.0 create [edge]") || !strings.Contains(errOut, "refresh failed for v0.25.0") {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}
