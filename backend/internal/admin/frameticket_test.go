package admin

import (
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestTickets() (*ticketStore, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	return &ticketStore{key: deriveTicketKey([]byte("k")), now: clock.now}, clock
}

func TestTicketRoundTripIsOneTime(t *testing.T) {
	s, _ := newTestTickets()
	sess := uuid.New()
	tok := s.mint(ticketPreview, sess, "e1", []byte("body"))
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`).MatchString(tok) {
		t.Fatalf("ticket shape: %q", tok)
	}
	body, ok := s.consume(ticketPreview, tok, sess, "e1")
	if !ok || string(body) != "body" {
		t.Fatalf("first use: %q %v", body, ok)
	}
	if _, ok := s.consume(ticketPreview, tok, sess, "e1"); ok {
		t.Fatal("a ticket was accepted twice")
	}
}

func TestTicketExpires(t *testing.T) {
	s, clock := newTestTickets()
	sess := uuid.New()
	tok := s.mint(ticketPreview, sess, "e1", nil)
	clock.t = clock.t.Add(ticketTTL + time.Second)
	if _, ok := s.consume(ticketPreview, tok, sess, "e1"); ok {
		t.Fatal("an expired ticket was accepted")
	}
	tok = s.mint(ticketPreview, sess, "e1", nil)
	clock.t = clock.t.Add(ticketTTL - time.Second)
	if _, ok := s.consume(ticketPreview, tok, sess, "e1"); !ok {
		t.Fatal("a ticket inside its window was refused")
	}
}

func TestTicketBindings(t *testing.T) {
	sess := uuid.New()
	cases := []struct {
		name    string
		purpose string
		sess    uuid.UUID
		target  string
	}{
		{"other session", ticketPreview, uuid.New(), "e1"},
		{"no session", ticketPreview, uuid.Nil, "e1"},
		{"other target", ticketPreview, sess, "e2"},
		{"other purpose", ticketPluginUI, sess, "e1"},
	}
	for _, c := range cases {
		s, _ := newTestTickets()
		tok := s.mint(ticketPreview, sess, "e1", nil)
		if _, ok := s.consume(c.purpose, tok, c.sess, c.target); ok {
			t.Errorf("%s: accepted", c.name)
		}
		if c.sess == uuid.Nil {
			continue // refused before any lookup, so nothing is spent
		}
		// A wrong attempt spends the ticket (no probing).
		if _, ok := s.consume(ticketPreview, tok, sess, "e1"); ok {
			t.Errorf("%s: the ticket survived a wrong attempt", c.name)
		}
	}
}

func TestTicketRejectsForgeryAndGarbage(t *testing.T) {
	s, _ := newTestTickets()
	sess := uuid.New()
	tok := s.mint(ticketPreview, sess, "e1", nil)
	tampered := []byte(tok)
	if tampered[31] == 'A' {
		tampered[31] = 'B'
	} else {
		tampered[31] = 'A'
	}
	for name, bad := range map[string]string{
		"tampered tag": string(tampered),
		"empty":        "",
		"short":        tok[:31],
		"long":         tok + "A",
		"not base64":   "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!",
		"zeros":        "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if _, ok := s.consume(ticketPreview, bad, sess, "e1"); ok {
			t.Errorf("%s accepted", name)
		}
	}
	// The tampered attempt hit the record, so the real one is spent too; a fresh
	// ticket from another store with another key must not verify either.
	other := &ticketStore{key: deriveTicketKey([]byte("other")), now: s.now}
	if _, ok := other.consume(ticketPreview, s.mint(ticketPreview, sess, "e1", nil), sess, "e1"); ok {
		t.Error("a ticket from another key was accepted")
	}
}

func TestTicketStoreIsBounded(t *testing.T) {
	s, clock := newTestTickets()
	sess := uuid.New()
	first := s.mint(ticketPreview, sess, "e", nil)
	for range ticketMax + 10 {
		clock.t = clock.t.Add(time.Millisecond) // eviction order follows expiry
		s.mint(ticketPreview, sess, "e", nil)
	}
	if len(s.m) > ticketMax {
		t.Fatalf("%d entries, cap %d", len(s.m), ticketMax)
	}
	if _, ok := s.consume(ticketPreview, first, sess, "e"); ok {
		t.Error("the oldest ticket survived eviction")
	}

	s, clock = newTestTickets()
	big := make([]byte, ticketMaxBytes/2+1)
	a := s.mint(ticketPreview, sess, "a", big)
	clock.t = clock.t.Add(time.Millisecond)
	s.mint(ticketPreview, sess, "b", big)
	if _, ok := s.consume(ticketPreview, a, sess, "a"); ok {
		t.Error("the byte bound did not evict the oldest body")
	}
}
