package spam

import (
	"strings"
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	key := TimingKey("secret")
	now := time.Unix(1_700_000_000, 0)
	tok := IssueToken(key, "contact", now)
	issued, ok := CheckToken(key, "contact", tok, now.Add(2*time.Second))
	if !ok || !issued.Equal(now) {
		t.Fatalf("CheckToken = %v, %v", issued, ok)
	}
}

func TestTokenRejected(t *testing.T) {
	key := TimingKey("secret")
	now := time.Unix(1_700_000_000, 0)
	tok := IssueToken(key, "contact", now)
	parts := strings.Split(tok, ".")
	forgedTS := "v1.1700000999." + parts[2]
	cases := map[string]struct {
		slug, tok string
		at        time.Time
		key       []byte
	}{
		"other slug":   {"other", tok, now, key},
		"other key":    {"contact", tok, now, TimingKey("rotated")},
		"forged ts":    {"contact", forgedTS, now.Add(time.Hour), key},
		"expired":      {"contact", tok, now.Add(25 * time.Hour), key},
		"future":       {"contact", tok, now.Add(-time.Hour), key},
		"empty":        {"contact", "", now, key},
		"garbage":      {"contact", "v1.x.y", now, key},
		"wrong prefix": {"contact", "v2." + parts[1] + "." + parts[2], now, key},
		"extra part":   {"contact", tok + ".x", now, key},
	}
	for name, c := range cases {
		if _, ok := CheckToken(c.key, c.slug, c.tok, c.at); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTokenYoungerThanMin(t *testing.T) {
	key := TimingKey("secret")
	now := time.Unix(1_700_000_000, 0)
	issued, ok := CheckToken(key, "f", IssueToken(key, "f", now), now.Add(time.Second))
	if !ok || now.Add(time.Second).Sub(issued) >= 3*time.Second {
		t.Fatal("1s-old token should be younger than 3s")
	}
}
