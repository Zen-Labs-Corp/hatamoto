package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// signedRelease builds a record, signs it and a manifest offering it, and
// returns the env and the file paths.
type signedRelease struct {
	env                                 env
	dir, record, sig, channel, artifact string
	manifestKey                         keyPair
}

func newSignedRelease(t *testing.T) signedRelease {
	t.Helper()
	dir := t.TempDir()
	s := signedRelease{dir: dir}
	s.record = buildRecordFile(t, dir)
	rel := newKeyPair(t, dir, "rel")
	s.manifestKey = newKeyPair(t, dir, "man")
	s.env = env{Release: rel.set, Manifest: s.manifestKey.set, Now: func() time.Time { return testNow }}
	runOK(t, s.env, "sign-record", s.record, "--key", rel.file)
	s.sig = s.record + ".sig"
	sigText, _ := os.ReadFile(s.sig)

	m := release.ChannelManifest{
		Channel:   "stable",
		IssuedAt:  testNow.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: testNow.Add(14 * 24 * time.Hour).Format(time.RFC3339),
		Releases:  []release.Entry{{Version: "v1.2.3", RecordSig: string(sigText[:len(sigText)-1]), Ramp: []release.RampStep{{At: testNow.Format(time.RFC3339), Pct: 100}}}},
		Yanked:    []release.Yank{},
	}
	body, _ := json.Marshal(m)
	doc, err := packages.SignIndex(body, s.manifestKey.key)
	if err != nil {
		t.Fatal(err)
	}
	s.channel = filepath.Join(dir, "stable.json")
	os.WriteFile(s.channel, doc, 0o644)
	s.artifact = filepath.Join(dir, "hatamoto-linux-amd64")
	return s
}

func (s signedRelease) verifyArgs() []string {
	return []string{"verify", "--chan", "stable", "--channel", s.channel, "--record", s.record, "--sig", s.sig, "--artifact", s.artifact, "--platform", "linux-amd64"}
}

func TestVerifyCommandAcceptsGood(t *testing.T) {
	s := newSignedRelease(t)
	runOK(t, s.env, s.verifyArgs()...)
	out := runOK(t, s.env, "verify", "--chan", "stable", "--manifest", s.channel)
	if out == "" {
		t.Fatal("no output")
	}
}

func TestVerifyCommandCanaryChannel(t *testing.T) {
	s := newSignedRelease(t) // manifest is signed for stable
	runErr(t, s.env, "verify", "--chan", "canary", "--manifest", s.channel)

	// A manifest signed for canary verifies under --chan canary.
	sigText, err := os.ReadFile(s.sig)
	if err != nil {
		t.Fatal(err)
	}
	m := release.ChannelManifest{
		Channel:   "canary",
		IssuedAt:  testNow.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: testNow.Add(14 * 24 * time.Hour).Format(time.RFC3339),
		Releases:  []release.Entry{{Version: "v1.2.3", RecordSig: strings.TrimSpace(string(sigText)), Ramp: []release.RampStep{{At: testNow.Format(time.RFC3339), Pct: 100}}}},
		Yanked:    []release.Yank{},
	}
	body, _ := json.Marshal(m)
	doc, err := packages.SignIndex(body, s.manifestKey.key)
	if err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(s.dir, "canary.json")
	if err := os.WriteFile(canary, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	runOK(t, s.env, "verify", "--chan", "canary", "--manifest", canary)
	runErr(t, s.env, "verify", "--chan", "stable", "--manifest", canary)
	runErr(t, s.env, "verify", "--chan", "nightly", "--manifest", s.channel)
}

func TestVerifyCommandRejectsTamper(t *testing.T) {
	s := newSignedRelease(t)
	os.WriteFile(s.artifact, []byte("tampered"), 0o755)
	runErr(t, s.env, s.verifyArgs()...)

	s = newSignedRelease(t)
	b, _ := os.ReadFile(s.record)
	os.WriteFile(s.record, append(b, ' '), 0o644)
	runErr(t, s.env, s.verifyArgs()...)

	s = newSignedRelease(t)
	runErr(t, env{Release: s.env.Release, Manifest: newKeyPair(t, s.dir, "else").set, Now: s.env.Now}, "verify", "--chan", "stable", "--manifest", s.channel)

	s = newSignedRelease(t)
	later := s.env
	later.Now = func() time.Time { return testNow.Add(30 * 24 * time.Hour) }
	runErr(t, later, "verify", "--chan", "stable", "--manifest", s.channel)
}

func TestVerifyRecordOnly(t *testing.T) {
	args := func(s signedRelease, extra ...string) []string {
		return append([]string{"verify", "--chan", "edge", "--record", s.record, "--sig", s.sig, "--dir", s.dir}, extra...)
	}
	ok := func() signedRelease { return newSignedRelease(t) }

	s := ok()
	out := runOK(t, s.env, append(args(s), "--version", "v1.2.3")...)
	if !strings.Contains(out, "record ok: v1.2.3 (4 artifacts)") {
		t.Fatalf("output: %q", out)
	}
	runOK(t, s.env, args(s)...) // --version is optional

	// A flipped byte names the artifact.
	s = ok()
	name := filepath.Join(s.dir, "hatamoto-linux-arm64")
	b, _ := os.ReadFile(name)
	b[0] ^= 1
	os.WriteFile(name, b, 0o755)
	if err := runErr(t, s.env, args(s)...); !strings.Contains(err.Error(), "hatamoto-linux-arm64") {
		t.Fatalf("error does not name the artifact: %v", err)
	}

	// Wrong version, missing artifact, record signed by another key.
	s = ok()
	runErr(t, s.env, append(args(s), "--version", "v9.9.9")...)
	s = ok()
	os.Remove(filepath.Join(s.dir, "hatamoto-darwin-amd64"))
	runErr(t, s.env, args(s)...)
	s = ok()
	other := s.env
	other.Release = newKeyPair(t, s.dir, "else").set
	runErr(t, other, args(s)...)
	// The manifest key is not a release key.
	s = ok()
	wrong := s.env
	wrong.Release = s.manifestKey.set
	runErr(t, wrong, args(s)...)

	// --dir needs --record and --sig; --dir with --channel is not a mode.
	s = ok()
	runErr(t, s.env, "verify", "--chan", "edge", "--dir", s.dir)
	runErr(t, s.env, append(s.verifyArgs(), "--dir", s.dir)...)
}
