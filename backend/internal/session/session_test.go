package session

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewOpaqueToken_Unique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := range 1000 {
		tok, err := newOpaqueToken()
		if err != nil {
			t.Fatalf("newOpaqueToken: %v", err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token after %d calls: %s", i, tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestNewOpaqueToken_Format(t *testing.T) {
	tok, err := newOpaqueToken()
	if err != nil {
		t.Fatalf("newOpaqueToken: %v", err)
	}
	// 32 random bytes encoded as raw URL base64 = 43 chars (no padding).
	if got := len(tok); got != 43 {
		t.Fatalf("token length: got %d, want 43 (32 bytes raw URL base64)", got)
	}
	// Must decode back to 32 bytes.
	dec, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(dec) != 32 {
		t.Fatalf("decoded length: got %d, want 32", len(dec))
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	const tok = "abc-123-DEF"
	h1 := hashToken(tok)
	h2 := hashToken(tok)
	if len(h1) != 32 {
		t.Fatalf("sha256 length: got %d, want 32", len(h1))
	}
	for i := range h1 {
		if h1[i] != h2[i] {
			t.Fatalf("hash differs at byte %d", i)
		}
	}
}

func TestHashToken_DifferentInputs(t *testing.T) {
	h1 := hashToken("foo")
	h2 := hashToken("foo ")
	for i := range h1 {
		if h1[i] != h2[i] {
			return // OK: different
		}
	}
	t.Fatal("hashes for 'foo' and 'foo ' are identical")
}

func TestSession_IsExpired(t *testing.T) {
	past := &Session{ExpiresAt: time.Now().Add(-1 * time.Second)}
	future := &Session{ExpiresAt: time.Now().Add(1 * time.Hour)}
	if !past.IsExpired() {
		t.Fatal("past session should be expired")
	}
	if future.IsExpired() {
		t.Fatal("future session should not be expired")
	}
}

func TestNullableString(t *testing.T) {
	if got := nullableString(""); got != nil {
		t.Fatalf("empty string should be nil, got %v", got)
	}
	if got := nullableString("ua"); got != "ua" {
		t.Fatalf("non-empty should pass through, got %v", got)
	}
}

func TestKind_Constants(t *testing.T) {
	// Lock the string values — they're written to the DB and the chk_sessions_kind
	// CHECK constraint encodes it as 'admin'.
	if KindAdmin != "admin" {
		t.Fatalf("KindAdmin: got %q, want admin", KindAdmin)
	}
	if KindAPI != "api" {
		t.Fatalf("KindAPI: got %q, want api", KindAPI)
	}
}

// Trivial smoke test to ensure the package's public types compile.
func TestSession_TypeShape(t *testing.T) {
	uid := uuid.New()
	a := &Session{Kind: KindAdmin, UserID: &uid}
	if a.UserID == nil {
		t.Fatal("admin session shape")
	}
}
