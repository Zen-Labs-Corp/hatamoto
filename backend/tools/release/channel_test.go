package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/release"
)

func TestChannelNamesMatchRelease(t *testing.T) {
	if !slices.Equal(channelNames, release.Channels) {
		t.Fatalf("channelNames = %v, release.Channels = %v", channelNames, release.Channels)
	}
}

func emptyChannel(name string) string {
	return "{\n  \"channel\": \"" + name + "\",\n  \"issued_at\": \"\",\n  \"expires_at\": \"\",\n  \"releases\": [],\n  \"yanked\": []\n}\n"
}

// channelFixture makes a channel dir, a signed record and a trusted release key.
type channelFixture struct {
	dir, chans, record, sig string
	rel                     keyPair
	e                       env
}

func newChannelFixture(t *testing.T) channelFixture {
	t.Helper()
	dir := t.TempDir()
	chans := filepath.Join(dir, "channels")
	if err := os.Mkdir(chans, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range channelNames {
		if err := os.WriteFile(filepath.Join(chans, c+".json"), []byte(emptyChannel(c)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := buildRecordFile(t, dir)
	rel := newKeyPair(t, dir, "rel")
	e := env{Release: rel.set, Now: func() time.Time { return testNow }}
	runOK(t, e, "sign-record", rec, "--key", rel.file)
	return channelFixture{dir: dir, chans: chans, record: rec, sig: rec + ".sig", rel: rel, e: e}
}

func (f channelFixture) source(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.chans, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestChannelAddRampHaltUnhaltYank(t *testing.T) {
	f := newChannelFixture(t)
	sigText, _ := os.ReadFile(f.sig)
	sig := strings.TrimSpace(string(sigText))

	runOK(t, f.e, "channel", "add", "edge", "v1.2.3", "--dir", f.chans,
		"--record", f.record, "--sig", f.sig, "--ramp", "now:5,+2d:25,+4d:100")
	want := `{
  "channel": "edge",
  "issued_at": "",
  "expires_at": "",
  "releases": [
    {
      "version": "v1.2.3",
      "record_sig": "` + sig + `",
      "ramp": [
        {
          "from": "2026-11-01T12:00:00Z",
          "pct": 5
        },
        {
          "from": "2026-11-03T12:00:00Z",
          "pct": 25
        },
        {
          "from": "2026-11-05T12:00:00Z",
          "pct": 100
        }
      ],
      "halted": false
    }
  ],
  "yanked": []
}
`
	if got := f.source(t, "edge"); got != want {
		t.Fatalf("after add:\n%s\nwant:\n%s", got, want)
	}

	runOK(t, f.e, "channel", "ramp", "edge", "v1.2.3", "--dir", f.chans, "--ramp", "now:100")
	if got := f.source(t, "edge"); !strings.Contains(got, `"pct": 100`) || strings.Contains(got, `"pct": 25`) {
		t.Fatalf("ramp not replaced:\n%s", got)
	}
	runOK(t, f.e, "channel", "halt", "edge", "v1.2.3", "--dir", f.chans, "--reason", "crash on boot")
	if got := f.source(t, "edge"); !strings.Contains(got, `"halted": true`) || !strings.Contains(got, `"halt_reason": "crash on boot"`) {
		t.Fatalf("not halted with a reason:\n%s", got)
	}
	runOK(t, f.e, "channel", "unhalt", "edge", "v1.2.3", "--dir", f.chans)
	if got := f.source(t, "edge"); !strings.Contains(got, `"halted": false`) || strings.Contains(got, "halt_reason") {
		t.Fatalf("not unhalted:\n%s", got)
	}
	f.addEntry(t, "edge", "v1.2.4")
	runOK(t, f.e, "channel", "yank", "edge", "v1.2.3", "--dir", f.chans, "--reason", "bad migration", "--to", "v1.2.4")
	got := f.source(t, "edge")
	for _, w := range []string{`"reason": "bad migration"`, `"to": "v1.2.4"`} {
		if !strings.Contains(got, w) {
			t.Errorf("yank missing %s:\n%s", w, got)
		}
	}
	if f.source(t, "stable") != emptyChannel("stable") {
		t.Error("the other channel changed")
	}
	runErr(t, f.e, "channel", "yank", "edge", "v1.2.3", "--dir", f.chans, "--reason", "again")
	runErr(t, f.e, "channel", "halt", "edge", "v9.9.9", "--dir", f.chans)
	runErr(t, f.e, "channel", "add", "beta", "v1.2.3", "--dir", f.chans)
}

// addEntry puts a bare entry for version into a channel source, standing in
// for a second signed release.
func (f channelFixture) addEntry(t *testing.T, name, version string) {
	t.Helper()
	path := filepath.Join(f.chans, name+".json")
	m, err := loadChannel(path, name)
	if err != nil {
		t.Fatal(err)
	}
	m.Releases = append(m.Releases, release.Entry{Version: version, RecordSig: "0123456789abcdef:c2ln", Ramp: []release.RampStep{{At: "2026-11-01T12:00:00Z", Pct: 100}}})
	if err := saveChannel(path, m); err != nil {
		t.Fatal(err)
	}
}

// TestChannelEditsRefuseWrongVersions: a yank or halt that names a version the
// channel does not hold (or spells it differently) must fail, never sign a no-op.
func TestChannelEditsRefuseWrongVersions(t *testing.T) {
	f := newChannelFixture(t)
	f.addEntry(t, "stable", "v1.4.1")
	f.addEntry(t, "stable", "v1.4.2")
	before := f.source(t, "stable")
	d := []string{"--dir", f.chans}
	bad := [][]string{
		{"yank", "stable", "v1.4.9", "--reason", "x"},                   // not in the channel
		{"yank", "stable", "1.4.1", "--reason", "x"},                    // no v
		{"yank", "stable", "v1.4.1+meta", "--reason", "x"},              // build metadata
		{"yank", "stable", "v1.4.1", "--reason", "x", "--to", "v1.4.9"}, // --to not in the channel
		{"yank", "stable", "v1.4.2", "--reason", "x", "--to", "v1.4.1"}, // --to older
		{"yank", "stable", "v1.4.1", "--reason", "x", "--to", "v1.4.1"}, // --to itself
		{"yank", "stable", "v1.4.1", "--reason", "x", "--to", "1.4.2"},  // --to not canonical
		{"halt", "stable", "v1.4.9"}, {"unhalt", "stable", "v1.4.9"}, {"halt", "stable", "1.4.1"},
		{"ramp", "stable", "v1.4.9", "--ramp", "now:100"},
	}
	for _, args := range bad {
		runErr(t, f.e, append(append([]string{"channel"}, args...), d...)...)
	}
	if f.source(t, "stable") != before {
		t.Fatal("a refused edit changed the source")
	}
	runOK(t, f.e, append([]string{"channel", "yank", "stable", "v1.4.1", "--reason", "x", "--to", "v1.4.2"}, d...)...)
}

func TestChannelAddRejectsBadSig(t *testing.T) {
	f := newChannelFixture(t)
	other := newKeyPair(t, f.dir, "other")
	badSig := filepath.Join(f.dir, "bad.sig")
	runOK(t, env{Release: other.set}, "sign-record", f.record, "--key", other.file, "--out", badSig)
	err := runErr(t, f.e, "channel", "add", "edge", "v1.2.3", "--dir", f.chans,
		"--record", f.record, "--sig", badSig, "--ramp", "now:100")
	if !strings.Contains(err.Error(), "bad.sig") {
		t.Errorf("error should name the sig file: %v", err)
	}
	if f.source(t, "edge") != emptyChannel("edge") {
		t.Error("channel changed after a refused add")
	}

	// A good signature over a different record is refused too.
	if err := os.WriteFile(f.record, append([]byte(" "), mustRead(t, f.record)...), 0o644); err != nil {
		t.Fatal(err)
	}
	runErr(t, f.e, "channel", "add", "edge", "v1.2.3", "--dir", f.chans,
		"--record", f.record, "--sig", f.sig, "--ramp", "now:100")
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestChannelAddRejectsDuplicate(t *testing.T) {
	f := newChannelFixture(t)
	args := []string{"channel", "add", "edge", "v1.2.3", "--dir", f.chans,
		"--record", f.record, "--sig", f.sig, "--ramp", "now:100"}
	runOK(t, f.e, args...)
	before := f.source(t, "edge")
	runErr(t, f.e, args...)
	if f.source(t, "edge") != before {
		t.Error("channel changed after a refused duplicate")
	}
}

func TestChannelCanaryAddHalt(t *testing.T) {
	f := newChannelFixture(t)
	runOK(t, f.e, "channel", "add", "canary", "v1.2.3", "--dir", f.chans,
		"--record", f.record, "--sig", f.sig, "--ramp", "now:100")
	if got := f.source(t, "canary"); !strings.Contains(got, `"channel": "canary"`) || !strings.Contains(got, `"version": "v1.2.3"`) {
		t.Fatalf("canary not added:\n%s", got)
	}
	runOK(t, f.e, "channel", "halt", "canary", "v1.2.3", "--dir", f.chans, "--reason", "bad")
	if got := f.source(t, "canary"); !strings.Contains(got, `"halted": true`) {
		t.Fatalf("canary not halted:\n%s", got)
	}
	for _, c := range []string{"edge", "stable"} {
		if f.source(t, c) != emptyChannel(c) {
			t.Errorf("%s changed by a canary edit", c)
		}
	}
	runErr(t, f.e, "channel", "add", "nightly", "v1.2.3", "--dir", f.chans,
		"--record", f.record, "--sig", f.sig, "--ramp", "now:100")
}

func TestResignRefusesChannelSwaps(t *testing.T) {
	f := newChannelFixture(t)
	man := newKeyPair(t, f.dir, "man")
	e := env{Release: f.rel.set, Manifest: man.set, Now: f.e.Now}
	out := filepath.Join(f.dir, "out")
	for _, c := range channelNames {
		runOK(t, e, "channel", "resign", "--channel", c, "--in", f.chans, "--out", out, "--key", man.file)
	}
	for _, signed := range channelNames {
		file := filepath.Join(out, signed+".json")
		for _, want := range channelNames {
			args := []string{"verify", "--chan", want, "--manifest", file}
			if signed == want {
				runOK(t, e, args...)
			} else {
				runErr(t, e, args...)
			}
		}
	}
}

func TestChannelAddRejectsVersionMismatch(t *testing.T) {
	f := newChannelFixture(t)
	runErr(t, f.e, "channel", "add", "edge", "v1.2.4", "--dir", f.chans,
		"--record", f.record, "--sig", f.sig, "--ramp", "now:100")
}

func TestParseRamp(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 34, 56, 0, time.UTC)
	got, err := parseRamp("now:5,+2d:25,+50h:100", now)
	if err != nil {
		t.Fatal(err)
	}
	want := []release.RampStep{
		{At: "2026-11-01T12:34:00Z", Pct: 5},
		{At: "2026-11-03T12:34:00Z", Pct: 25},
		{At: "2026-11-03T14:34:00Z", Pct: 100},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d: %v, want %v", i, got[i], want[i])
		}
	}
	for _, schedule := range []string{"now:100", "+2d:100", "+7d:5,+9d:25,+11d:100"} {
		if _, err := parseRamp(schedule, now); err != nil {
			t.Errorf("parseRamp(%q): %v", schedule, err)
		}
	}
	for _, bad := range []string{
		"", "now", "now:0", "now:101", "now:x", "now:50,+1d:40", "now:50,now:60",
		"+d:5", "+1x:5", "-1d:5", "tomorrow:5", "+2d:25,now:50",
	} {
		if _, err := parseRamp(bad, now); err == nil {
			t.Errorf("parseRamp(%q) should fail", bad)
		}
	}
}

func TestResignRoundTrip(t *testing.T) {
	f := newChannelFixture(t)
	runOK(t, f.e, "channel", "add", "edge", "v1.2.3", "--dir", f.chans,
		"--record", f.record, "--sig", f.sig, "--ramp", "now:100")
	man := newKeyPair(t, f.dir, "man")
	e := env{Release: f.rel.set, Manifest: man.set, Now: f.e.Now}
	out := filepath.Join(f.dir, "out")
	for _, c := range channelNames {
		runOK(t, e, "channel", "resign", "--channel", c, "--in", f.chans, "--out", out, "--key", man.file)
	}
	doc := mustRead(t, filepath.Join(out, "edge.json"))
	m, err := release.VerifyManifest(doc, release.Options{Channel: "edge", Manifest: man.set, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	issued, err1 := time.Parse(time.RFC3339, m.IssuedAt)
	expires, err2 := time.Parse(time.RFC3339, m.ExpiresAt)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !issued.Equal(testNow) || expires.Sub(issued) != 14*24*time.Hour {
		t.Errorf("window %s .. %s", m.IssuedAt, m.ExpiresAt)
	}
	if len(m.Releases) != 1 || m.Releases[0].Version != "v1.2.3" {
		t.Errorf("releases: %+v", m.Releases)
	}
	late := release.Options{Channel: "edge", Manifest: man.set, Now: func() time.Time { return testNow.Add(15 * 24 * time.Hour) }}
	if _, err := release.VerifyManifest(doc, late); err == nil {
		t.Error("a manifest 15 days old must be refused")
	}
	// The file passes the tool's own verify command too.
	runOK(t, e, "verify", "--chan", "stable", "--manifest", filepath.Join(out, "stable.json"))
	// resign binds each file to its channel.
	if m.Channel != "edge" {
		t.Errorf("edge.json says channel %q", m.Channel)
	}
	runErr(t, e, "verify", "--chan", "edge", "--manifest", filepath.Join(out, "stable.json"))
	// A release key is not a manifest key.
	runErr(t, e, "channel", "resign", "--channel", "edge", "--in", f.chans, "--out", out, "--key", f.rel.file)
}

func TestResignDoesNotModifySource(t *testing.T) {
	f := newChannelFixture(t)
	man := newKeyPair(t, f.dir, "man")
	e := env{Manifest: man.set, Now: f.e.Now}
	runOK(t, e, "channel", "resign", "--channel", "stable", "--in", f.chans,
		"--out", filepath.Join(f.dir, "out"), "--key", man.file)
	if f.source(t, "stable") != emptyChannel("stable") {
		t.Error("resign rewrote the source file")
	}
}

func TestCommittedChannelSourcesParse(t *testing.T) {
	// The public tree has no channel state (export-ignore, see scripts/test-channels-private.sh,
	// which asserts the private repository does track these files).
	if _, err := os.Stat(filepath.Join("..", "..", "..", "releases", "channels")); os.IsNotExist(err) {
		t.Skip("releases/channels is not in this repository (public tree)")
	}
	for _, c := range channelNames {
		m, err := loadChannel(filepath.Join("..", "..", "..", "releases", "channels", c+".json"), c)
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if m.IssuedAt != "" || m.ExpiresAt != "" {
			t.Errorf("%s: committed source must not carry times", c)
		}
		if m.Releases == nil || m.Yanked == nil {
			t.Errorf("%s: releases and yanked must be [] not null", c)
		}
	}
}

// TestChannelSourceIsStrict: every way a hand edit can go wrong is refused by
// resign (and by every other channel command, which load the same way).
func TestChannelSourceIsStrict(t *testing.T) {
	f := newChannelFixture(t)
	man := newKeyPair(t, f.dir, "man")
	e := env{Release: f.rel.set, Manifest: man.set, Now: f.e.Now}
	sig := "0123456789abcdef:c2ln"
	rel := func(ramp string) string {
		return `"releases":[{"version":"v1.2.3","record_sig":"` + sig + `","ramp":[` + ramp + `],"halted":false}],"yanked":[]`
	}
	head := `{"channel":"edge","issued_at":"","expires_at":"",`
	good := `{"from":"2026-11-01T12:00:00Z","pct":5},{"from":"2026-11-02T12:00:00Z","pct":25}`
	cases := map[string]string{
		"unknown field (the old name)": head + rel(`{"`+"a"+`t":"2026-11-01T12:00:00Z","pct":5}`) + `}`,
		"typo in a key":                head + `"releases":[{"version":"v1.2.3","record_sig":"` + sig + `","ramp":[` + good + `],"haltd":true}],"yanked":[]}`,
		"pct as text":                  head + rel(`{"from":"2026-11-01T12:00:00Z","pct":"5"}`) + `}`,
		"pct falls":                    head + rel(`{"from":"2026-11-01T12:00:00Z","pct":50},{"from":"2026-11-02T12:00:00Z","pct":25}`) + `}`,
		"pct over 100":                 head + rel(`{"from":"2026-11-01T12:00:00Z","pct":101}`) + `}`,
		"empty time":                   head + rel(`{"from":"","pct":5}`) + `}`,
		"time is not RFC 3339":         head + rel(`{"from":"2026-11-01","pct":5}`) + `}`,
		"times equal":                  head + rel(`{"from":"2026-11-01T12:00:00Z","pct":5},{"from":"2026-11-01T12:00:00Z","pct":6}`) + `}`,
		"times fall":                   head + rel(`{"from":"2026-11-02T12:00:00Z","pct":5},{"from":"2026-11-01T12:00:00Z","pct":6}`) + `}`,
		"version without v":            strings.Replace(head+rel(good)+`}`, `"v1.2.3"`, `"1.2.3"`, 1),
		"record_sig without key id":    strings.Replace(head+rel(good)+`}`, sig, "c2ln", 1),
		"record_sig not base64":        strings.Replace(head+rel(good)+`}`, sig, "0123456789abcdef:%%%", 1),
		"duplicate version":            head + `"releases":[{"version":"v1.2.3","record_sig":"` + sig + `","ramp":[` + good + `]},{"version":"v1.2.3","record_sig":"` + sig + `","ramp":[` + good + `]}],"yanked":[]}`,
		"empty ramp":                   head + rel(``) + `}`,
		"pct 0":                        head + rel(`{"from":"2026-11-01T12:00:00Z","pct":0}`) + `}`,
		"yank of an unlisted version":  strings.Replace(head+rel(good)+`}`, `"yanked":[]`, `"yanked":[{"version":"v1.0.0","reason":"r"}]`, 1),
		"yank to an unlisted version":  strings.Replace(head+rel(good)+`}`, `"yanked":[]`, `"yanked":[{"version":"v1.2.3","reason":"r","to":"v1.2.4"}]`, 1),
		"halt_reason without halt":     strings.Replace(head+rel(good)+`}`, `"halted":false`, `"halted":false,"halt_reason":"x"`, 1),
		"yank not canonical":           head + `"releases":[],"yanked":[{"version":"v1.2.3+x","reason":"r"}]}`,
		"other channel's file":         strings.Replace(head+rel(good)+`}`, `"edge"`, `"stable"`, 1),
		"no channel field":             strings.Replace(head+rel(good)+`}`, `"channel":"edge",`, "", 1),
		"trailing data":                head + rel(good) + `}{}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(f.chans, "edge.json"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			runErr(t, e, "channel", "resign", "--channel", "edge", "--in", f.chans, "--out", filepath.Join(f.dir, "out"), "--key", man.file)
		})
	}
	// The same shapes, fixed, are accepted.
	ok := head + rel(good) + `}`
	if err := os.WriteFile(filepath.Join(f.chans, "edge.json"), []byte(ok), 0o644); err != nil {
		t.Fatal(err)
	}
	runOK(t, e, "channel", "resign", "--channel", "edge", "--in", f.chans, "--out", filepath.Join(f.dir, "out"), "--key", man.file)
}
