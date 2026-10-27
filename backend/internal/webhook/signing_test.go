package webhook

import (
	"strings"
	"testing"
	"time"
)

func TestSignKnownAnswer(t *testing.T) {
	// openssl dgst -sha256 -hmac key  <<< "The quick brown fox jumps over the lazy dog"
	got := Sign("key", []byte("The quick brown fox jumps over the lazy dog"))
	if want := "sha256=f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"; got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestSignV2KnownAnswer(t *testing.T) {
	// printf '1700000000.body' | openssl dgst -sha256 -hmac key
	got := SignV2("key", 1700000000, []byte("body"))
	if want := "t=1700000000,v1=47b6ce0fca59474308e2921c247cb2493dce6b8101d90ac05bd0c6a37d0e046e"; got != want {
		t.Fatalf("SignV2 = %s, want %s", got, want)
	}
	if SignV2("key", 1700000001, []byte("body")) == got {
		t.Fatal("a different timestamp must change the signature")
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	for _, c := range []struct {
		attempt int
		base    time.Duration
		want    time.Duration
	}{
		{1, 60 * time.Second, 2 * time.Minute}, {2, 60 * time.Second, 4 * time.Minute},
		{3, 60 * time.Second, 8 * time.Minute}, {10, 60 * time.Second, time.Hour}, {1, 0, 0},
	} {
		if got := Backoff(c.attempt, c.base); got != c.want {
			t.Errorf("Backoff(%d, %s) = %s, want %s", c.attempt, c.base, got, c.want)
		}
	}
}

func TestEnvelopeKeyOrder(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.FixedZone("x", 3600))
	b, err := NewEnvelope("id-1", EventEntityPublished, now, map[string]any{"entity_id": "e", "entity_name": "n"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	want := `{"id":"id-1","event":"entity.published","timestamp":"2026-10-19T11:00:00Z","data":{"entity_id":"e","entity_name":"n"}}`
	if s != want {
		t.Fatalf("body = %s\nwant   %s", s, want)
	}
	if !strings.HasPrefix(s, `{"id":`) {
		t.Fatal("id must come first")
	}
	last := -1
	for _, k := range []string{`"id"`, `"event"`, `"timestamp"`, `"data"`} {
		i := strings.Index(s, k)
		if i <= last {
			t.Fatalf("%s out of order in %s", k, s)
		}
		last = i
	}
}
