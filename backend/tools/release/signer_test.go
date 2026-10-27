package main

import (
	"crypto/ed25519"
	"net"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/agent"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

func TestSignRecordWithKeyFile(t *testing.T) {
	dir := t.TempDir()
	rec := buildRecordFile(t, dir)
	kp := newKeyPair(t, dir, "rel")
	e := env{Release: kp.set}
	runOK(t, e, "sign-record", rec, "--key", kp.file)

	recBytes, _ := os.ReadFile(rec)
	sig, err := os.ReadFile(rec + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	id, b64, err := release.SplitRecordSig(string(sig))
	if err != nil {
		t.Fatal(err)
	}
	if err := kp.set.VerifyBytes(id, "", b64, recBytes); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(sig), "\n") {
		t.Error("signature file lacks a trailing newline")
	}
}

func TestSignRecordRefusesUntrustedKey(t *testing.T) {
	dir := t.TempDir()
	rec := buildRecordFile(t, dir)
	kp, other := newKeyPair(t, dir, "rel"), newKeyPair(t, dir, "other")
	err := runErr(t, env{Release: kp.set}, "sign-record", rec, "--key", other.file)
	if !strings.Contains(err.Error(), "not a trusted release key") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(rec + ".sig"); err == nil {
		t.Fatal("a signature was written for an untrusted key")
	}
	// The embedded set refuses a throwaway key too.
	runErr(t, env{}, "sign-record", rec, "--key", kp.file)
}

// startAgent serves a keyring holding keys over an in-memory connection and
// returns an env whose AgentConn dials it.
func startAgent(t *testing.T, e env, keys ...packages.Key) env {
	t.Helper()
	ring := agent.NewKeyring()
	for _, k := range keys {
		priv, err := k.PrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := ring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
			t.Fatal(err)
		}
	}
	e.AgentConn = func() (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			agent.ServeAgent(ring, server)
			server.Close()
		}()
		return client, nil
	}
	return e
}

func TestSignRecordWithAgent(t *testing.T) {
	dir := t.TempDir()
	rec := buildRecordFile(t, dir)
	kp, stranger := newKeyPair(t, dir, "rel"), newKeyPair(t, dir, "stranger")
	e := startAgent(t, env{Release: kp.set}, stranger.key, kp.key)
	runOK(t, e, "sign-record", rec, "--agent")

	recBytes, _ := os.ReadFile(rec)
	sig, _ := os.ReadFile(rec + ".sig")
	id, b64, err := release.SplitRecordSig(string(sig))
	if err != nil {
		t.Fatal(err)
	}
	if id != kp.key.ID {
		t.Fatalf("signed with %s, want the trusted key %s", id, kp.key.ID)
	}
	if err := kp.set.VerifyBytes(id, "", b64, recBytes); err != nil {
		t.Fatal(err)
	}

	// --agent-key narrows the choice; an id the agent lacks fails.
	runOK(t, e, "sign-record", rec, "--agent", "--agent-key", kp.key.ID)
	runErr(t, e, "sign-record", rec, "--agent", "--agent-key", "0000000000000000")

	// An agent holding only an untrusted key is refused.
	only := startAgent(t, env{Release: kp.set}, stranger.key)
	runErr(t, only, "sign-record", rec, "--agent")
}

func TestAgentSignerSignsRawEd25519(t *testing.T) {
	dir := t.TempDir()
	kp := newKeyPair(t, dir, "rel")
	e := startAgent(t, env{}, kp.key)
	conn, _ := e.AgentConn()
	defer conn.Close()
	s, err := newAgentSigner(agent.NewClient(conn), kp.set, "")
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello")
	raw, err := s.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(s.PublicKey(), msg, raw) {
		t.Fatal("agent blob is not a plain ed25519 signature")
	}
}

func TestSignRecordUsageErrors(t *testing.T) {
	dir := t.TempDir()
	rec := buildRecordFile(t, dir)
	kp := newKeyPair(t, dir, "rel")
	runErr(t, env{Release: kp.set}, "sign-record", rec)
	runErr(t, env{Release: kp.set}, "sign-record", rec, "--key", kp.file, "--agent")
	runErr(t, env{Release: kp.set}, "sign-record", "--key", kp.file)
	runErr(t, env{Release: kp.set, AgentConn: func() (net.Conn, error) { return nil, os.ErrNotExist }}, "sign-record", rec, "--agent")
}

func TestSignRecordRefusesFailingRecord(t *testing.T) {
	dir := t.TempDir()
	rec := buildRecordFile(t, dir)
	kp := newKeyPair(t, dir, "rel")
	e := env{Release: kp.set}
	r, err := readRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	r.RollbackFloor = r.Version // the placeholder CI writes
	b, _ := release.MarshalRecord(r)
	os.WriteFile(rec, b, 0o644)
	err = runErr(t, e, "sign-record", rec, "--key", kp.file)
	if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(rec + ".sig"); err == nil {
		t.Fatal("a signature was written for a failing record")
	}
	runOK(t, e, "sign-record", rec, "--key", kp.file, "--allow-same")
	// Not JSON at all.
	os.WriteFile(rec, []byte("hello"), 0o644)
	runErr(t, e, "sign-record", rec, "--key", kp.file, "--allow-same")
}
