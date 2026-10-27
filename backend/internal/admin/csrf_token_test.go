package admin

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newKeyed(t *testing.T, flash string) *Admin {
	t.Helper()
	a, err := New(Deps{FlashKey: []byte(flash)})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTokenDerivation(t *testing.T) {
	a := newKeyed(t, "0123456789abcdef0123456789abcdef")
	id, other := uuid.New(), uuid.New()
	tok := a.sessionToken(id)
	if tok == "" || tok != a.sessionToken(id) {
		t.Fatalf("not deterministic: %q", tok)
	}
	if tok == a.sessionToken(other) {
		t.Error("tokens do not differ by id")
	}
	if a.mac("s", id.String()) == a.mac("p", id.String()) {
		t.Error("tokens do not differ by kind")
	}
	if tok == newKeyed(t, "fedcba9876543210fedcba9876543210").sessionToken(id) {
		t.Error("tokens do not differ by key")
	}
	if tok != newKeyed(t, "0123456789abcdef0123456789abcdef").sessionToken(id) {
		t.Error("same FlashKey must yield the same token across New")
	}
	// The domain label separates the CSRF key from the flash key itself.
	if string(a.csrfKey) == "0123456789abcdef0123456789abcdef" {
		t.Error("csrf key equals the flash key")
	}
	if a.preToken(id.String()) == "" {
		t.Error("empty pre token")
	}
	a.csrfTokenFn = func(kind, id string) string { return kind + "|" + id }
	if got := a.preToken("x"); got != "p|x" {
		t.Errorf("seam ignored: %q", got)
	}
}

func TestPreIDParse(t *testing.T) {
	a := newKeyed(t, "k")
	now := time.Now()
	fresh := a.newPreID(now.Add(time.Hour))
	if !a.parsePreID(fresh, now) {
		t.Errorf("fresh id refused: %q", fresh)
	}
	if a.parsePreID(a.newPreID(now.Add(-time.Second)), now) {
		t.Error("expired id accepted")
	}
	for _, v := range []string{"", "abc", ".123", fresh + "x.1", "short.9999999999", "AAAAAAAAAAAAAAAAAAAAAA.", "AAAAAAAAAAAAAAAAAAAAAA.nope", "AAAAAAAAAAAAAAAAAAAAAAA.9999999999"} {
		if a.parsePreID(v, now) {
			t.Errorf("malformed id accepted: %q", v)
		}
	}
}

func TestTokenEqual(t *testing.T) {
	for _, c := range []struct {
		got, want string
		eq        bool
	}{{"", "", false}, {"a", "", false}, {"", "a", false}, {"a", "ab", false}, {"ab", "ab", true}, {"ab", "ac", false}} {
		if tokenEqual(c.got, c.want) != c.eq {
			t.Errorf("tokenEqual(%q, %q) != %v", c.got, c.want, c.eq)
		}
	}
}
