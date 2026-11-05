package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Byte pins for state.json and events.jsonl as the previous encoder wrote
// them. state.json is read by a bootstrap supervisor that may be older than
// the release that wrote it, so the shape must not drift. A nil bad or
// plugin_errors list may later be written as [] instead of null: that case is
// pinned by decoding, never by bytes.

const (
	headStateNulls = "{\n  \"v\": 1,\n  \"phase\": \"idle\",\n  \"run\": \"bootstrap\",\n  \"current_seen\": \"v1.0.0\",\n  \"previous_is_bootstrap\": false,\n  \"attempts\": 0,\n  \"last_error\": \"\",\n  \"bad\": null,\n  \"bootstrap_sha256\": \"abc\",\n  \"smoke_token\": \"tok\",\n  \"plugin_errors\": null,\n  \"baseline_at\": \"0001-01-01T00:00:00Z\"\n}\n"
	headStateFull  = "{\n  \"v\": 1,\n  \"phase\": \"probation\",\n  \"run\": \"release\",\n  \"current_seen\": \"v1.0.1\",\n  \"previous_is_bootstrap\": true,\n  \"attempts\": 2,\n  \"last_error\": \"smoke: HTTP 500\",\n  \"bad\": [\n    {\n      \"version\": \"v1.0.2\",\n      \"reason\": \"smoke failed\",\n      \"when\": \"2026-11-01T10:00:00Z\"\n    }\n  ],\n  \"bootstrap_sha256\": \"abc\",\n  \"bootstrap_version\": \"v1.0.0\",\n  \"smoke_token\": \"tok\",\n  \"plugin_errors\": [\n    \"forms: boom\"\n  ],\n  \"smoke_baseline\": {\n    \"admin\": 302,\n    \"home\": 200\n  },\n  \"baseline_at\": \"2026-11-01T10:00:00Z\",\n  \"render_baseline\": 3,\n  \"known_boot_faults\": [\n    \"theme-fallback\"\n  ],\n  \"pending\": {\n    \"kind\": \"apply\",\n    \"version\": \"v1.0.3\",\n    \"from\": \"v1.0.1\",\n    \"prev_before\": \"bootstrap\",\n    \"before\": {\n      \"current\": \"v1.0.1\",\n      \"previous\": \"bootstrap\",\n      \"previous_is_bootstrap\": true,\n      \"run\": \"release\",\n      \"current_seen\": \"v1.0.1\"\n    },\n    \"requested_at\": \"2026-11-01T10:00:00Z\",\n    \"started_at\": \"2026-11-01T10:01:00Z\"\n  },\n  \"unproven\": \"v1.0.1\",\n  \"offline_rollback\": \"v1.0.0\",\n  \"ungated\": {\n    \"kind\": \"rollback\",\n    \"version\": \"v1.0.0\",\n    \"from\": \"\",\n    \"prev_before\": \"\",\n    \"requested_at\": \"2026-11-01T10:00:00Z\",\n    \"started_at\": \"0001-01-01T00:00:00Z\"\n  }\n}\n"
	headEventLine  = "{\"id\":\"1-abcd1234\",\"from\":\"v1.0.0\",\"to\":\"v1.0.1\",\"phase\":\"rolled_back\",\"error\":\"smoke: HTTP 500\\nsecond \\\"line\\\" é\",\"started_at\":\"2026-11-01T10:00:00Z\",\"finished_at\":\"2026-11-01T10:00:02Z\"}\n"
	headEventLines = "{\"id\":\"1-abcd1234\",\"from\":\"v1.0.0\",\"to\":\"v1.0.1\",\"phase\":\"rolled_back\",\"error\":\"smoke: HTTP 500\\nsecond \\\"line\\\" é\",\"started_at\":\"2026-11-01T10:00:00Z\",\"finished_at\":\"2026-11-01T10:00:02Z\"}\n{\"id\":\"2-abcd1234\",\"from\":\"bootstrap\",\"to\":\"v1.0.2\",\"phase\":\"success\",\"error\":\"\",\"started_at\":\"2026-11-01T10:00:00Z\",\"finished_at\":\"2026-11-01T10:00:00Z\"}\n"
)

var pinT0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func pinFullState() *State {
	return &State{V: 1, Phase: "probation", Run: "release", CurrentSeen: "v1.0.1", PreviousIsBootstrap: true, Attempts: 2, LastError: "smoke: HTTP 500",
		Bad: []BadEntry{{Version: "v1.0.2", Reason: "smoke failed", At: pinT0}}, BootstrapSHA256: "abc", BootstrapVersion: "v1.0.0", SmokeToken: "tok",
		PluginErrors: []string{"forms: boom"}, SmokeBaseline: map[string]int{"home": 200, "admin": 302}, BaselineAt: pinT0, RenderBaseline: 3,
		BootFaultBaseline: []string{"theme-fallback"},
		Pending: &Pending{Kind: "apply", Version: "v1.0.3", From: "v1.0.1", PrevBefore: "bootstrap",
			Before:      &LinkSnapshot{Current: "v1.0.1", Previous: "bootstrap", PreviousIsBootstrap: true, Run: "release", CurrentSeen: "v1.0.1"},
			RequestedAt: pinT0, StartedAt: pinT0.Add(time.Minute)},
		Unproven: "v1.0.1", OfflineRollback: "v1.0.0", Ungated: &Pending{Kind: "rollback", Version: "v1.0.0", RequestedAt: pinT0}}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHeadStateJSONLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writeFile(t, path, headStateNulls)
	s, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.V != 1 || s.Phase != "idle" || s.Run != "bootstrap" || s.CurrentSeen != "v1.0.0" || s.BootstrapSHA256 != "abc" || s.SmokeToken != "tok" ||
		len(s.Bad) != 0 || len(s.PluginErrors) != 0 || s.RenderBaseline != 0 || s.SmokeBaseline != nil || s.Pending != nil || !s.BaselineAt.IsZero() {
		t.Fatalf("state = %+v", s)
	}
	writeFile(t, path, headStateFull)
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := pinFullState(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %+v\nwant   %+v", got, want)
	}
}

func TestSaveStateBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := SaveState(path, pinFullState()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != headStateFull {
		t.Fatalf("state.json bytes changed:\n got %q\nwant %q", b, headStateFull)
	}
	// Nil bad and plugin_errors: only the decoded result is pinned.
	nils := &State{V: 1, Phase: "idle", Run: "bootstrap", CurrentSeen: "v1.0.0", BootstrapSHA256: "abc", SmokeToken: "tok"}
	if err := SaveState(path, nils); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Run != "bootstrap" || got.CurrentSeen != "v1.0.0" || got.SmokeToken != "tok" || len(got.Bad) != 0 || len(got.PluginErrors) != 0 || got.RenderBaseline != 0 {
		t.Fatalf("round trip = %+v", got)
	}
}

// One event is one line ending in a newline. The pinned error has no HTML
// characters: those are escaped by the previous encoder and may not be later.
func TestEventLineBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	e1 := Event{ID: "1-abcd1234", From: "v1.0.0", To: "v1.0.1", Phase: PhaseRolledBack, Error: "smoke: HTTP 500\nsecond \"line\" é", StartedAt: pinT0, FinishedAt: pinT0.Add(2 * time.Second)}
	if err := AppendEvent(path, e1); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != headEventLine {
		t.Fatalf("event line changed:\n got %q\nwant %q", b, headEventLine)
	}
	if err := AppendEvent(path, Event{ID: "2-abcd1234", From: "bootstrap", To: "v1.0.2", Phase: PhaseSuccess, StartedAt: pinT0, FinishedAt: pinT0}); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(path); string(b) != headEventLines {
		t.Fatalf("event lines changed:\n got %q\nwant %q", b, headEventLines)
	}
}

// rollback_floor is read from a stored release.json with the tolerant decoder:
// failing open would drop the floor and let a downgrade through.
func TestStoredFloorTolerant(t *testing.T) {
	l := NewLayout(t.TempDir())
	for name, c := range map[string]struct{ body, want string }{
		"plain":           {`{"version":"v1.4.0","rollback_floor":"v1.3.0"}`, "v1.3.0"},
		"odd case":        {`{"VERSION":"v1.4.0","Rollback_Floor":"v1.3.0"}`, "v1.3.0"},
		"repeated member": {`{"rollback_floor":"v1.3.0","rollback_floor":"v1.3.0"}`, "v1.3.0"},
		"repeated other":  {`{"notes_url":"a","notes_url":"b","rollback_floor":"v1.3.0"}`, "v1.3.0"},
		"unknown members": {`{"future":{"x":[1,2]},"rollback_floor":"v1.3.0","more":null}`, "v1.3.0"},
		"missing":         {`{"version":"v1.4.0"}`, ""},
		"not json":        {`rollback_floor`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(l.Releases, "v1.4.0")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "release.json"), c.body)
			if got := storedFloor(l, "v1.4.0"); got != c.want {
				t.Fatalf("floor = %q, want %q", got, c.want)
			}
		})
	}
}
