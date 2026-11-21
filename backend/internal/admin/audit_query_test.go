package admin

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAuditCursorRoundTripKeepsMicroseconds(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 2, 8, 30, 15, 123456000, time.UTC)
	for _, src := range []string{auditSrcAdmin, auditSrcAgent} {
		got := decodeAuditCursor(AuditCursor{At: at, Src: src, ID: 987654321}.Encode())
		if got == nil || !got.At.Equal(at) || got.Src != src || got.ID != 987654321 {
			t.Fatalf("round trip %s = %+v", src, got)
		}
	}
}

func TestDecodeAuditCursorRejectsMalformed(t *testing.T) {
	t.Parallel()

	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	now := strconv36(time.Now().UnixMicro())
	for name, in := range map[string]string{
		"empty":       "",
		"garbage":     "!!not base64!!",
		"bad number":  enc("x.a.1"),
		"bad src":     enc(now + ".z.1"),
		"zero id":     enc(now + ".a.0"),
		"negative id": enc(now + ".a.-5"),
		"two parts":   enc(now + ".a"),
		"far future":  enc(strconv36(time.Now().Add(72*time.Hour).UnixMicro()) + ".a.1"),
		"before 2000": enc(strconv36(time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()) + ".a.1"),
		"too long":    strings.Repeat("A", 65),
	} {
		if got := decodeAuditCursor(in); got != nil {
			t.Errorf("%s: decoded %+v", name, got)
		}
	}
	if got := decodeAuditCursor(enc(now + ".m.1")); got == nil {
		t.Error("a valid cursor was rejected")
	}
}

func strconv36(n int64) string { return strconv.FormatInt(n, 36) }

func TestEscapeLike(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"plain": "plain", `50%`: `50\%`, "a_b": `a\_b`, `a\b`: `a\\b`, `%_\`: `\%\_\\`, "": "",
	} {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAuditQuery(t *testing.T) {
	t.Parallel()

	const id = "0b0c5b7e-5d7e-4a53-8b8c-2f1d6f0b9a11"
	for _, c := range []struct {
		actor, want string
	}{
		{"u:not-a-uuid", ""}, {"k:" + id, "k:" + id}, {"u:" + id, "u:" + id},
		{"n:", ""}, {"n:jane@example.com", "n:jane@example.com"}, {"x:" + id, ""}, {id, ""},
	} {
		if got := normalizeAuditQuery(AuditQuery{Actor: c.actor}).Actor; got != c.want {
			t.Errorf("actor %q -> %q, want %q", c.actor, got, c.want)
		}
	}
	if got := normalizeAuditQuery(AuditQuery{Source: "agent"}).Source; got != "agent" {
		t.Errorf("source agent -> %q", got)
	}
	if got := normalizeAuditQuery(AuditQuery{Source: "root"}).Source; got != "" {
		t.Errorf("source root -> %q", got)
	}
	q := normalizeAuditQuery(AuditQuery{Q: "  " + strings.Repeat("é", 150) + "  "})
	if n := len([]rune(q.Q)); n != 100 {
		t.Errorf("Q has %d runes, want 100", n)
	}
	if got := normalizeAuditQuery(AuditQuery{Q: "  hi "}).Q; got != "hi" {
		t.Errorf("Q = %q", got)
	}
}
