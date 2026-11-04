package twofactor

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPendingRoundTrip(t *testing.T) {
	k := PendingKeyFor("server-secret")
	now := time.Unix(1_700_000_000, 0)
	uid := uuid.New()
	p, err := NewPending(uid, "/admin/pages", now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := k.Open(k.Seal(p), now.Add(4*time.Minute))
	if err != nil || got.UserID != uid || got.Next != "/admin/pages" || got.ID == "" {
		t.Fatalf("open = %+v %v", got, err)
	}
	p2, _ := NewPending(uid, "", now)
	if p2.ID == p.ID {
		t.Fatal("pending ids must be random")
	}
}

func TestPendingRefusals(t *testing.T) {
	k := PendingKeyFor("server-secret")
	now := time.Unix(1_700_000_000, 0)
	p, _ := NewPending(uuid.New(), "", now)
	tok := k.Seal(p)

	if _, err := k.Open(tok, now.Add(PendingTTL)); err == nil {
		t.Error("an expired token was accepted")
	}
	if _, err := PendingKeyFor("other").Open(tok, now); err == nil {
		t.Error("a token signed with another key was accepted")
	}
	payload, sig, _ := strings.Cut(tok, ".")
	for name, bad := range map[string]string{
		"tampered payload": payload + "x." + sig,
		"tampered sig":     payload + "." + sig[:len(sig)-2] + "AA",
		"no sig":           payload,
		"empty":            "",
		"dot only":         ".",
		"garbage":          "a.b",
	} {
		if _, err := k.Open(bad, now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A payload re-signed by nobody: swap the user in the payload keeping the old signature.
	other, _ := NewPending(uuid.New(), "", now)
	op, _, _ := strings.Cut(k.Seal(other), ".")
	if _, err := k.Open(op+"."+sig, now); err == nil {
		t.Error("a swapped payload kept a valid signature")
	}
	if _, err := PendingKey(nil).Open(tok, now); err == nil {
		t.Error("an empty key must accept nothing")
	}
}
