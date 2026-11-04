package admin

import (
	"regexp"
	"strings"
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

func TestChangesetPreviewTicket(t *testing.T) {
	sess := uuid.New()
	target := changesetTarget(42, 1)
	if target != "cs:42:1" {
		t.Fatalf("target = %q", target)
	}
	t.Run("once only", func(t *testing.T) {
		s, _ := newTestTickets()
		tok := s.mint(ticketChangesetPreview, sess, target, nil)
		if _, ok := s.consume(ticketChangesetPreview, tok, sess, target); !ok {
			t.Fatal("first use refused")
		}
		if _, ok := s.consume(ticketChangesetPreview, tok, sess, target); ok {
			t.Fatal("accepted twice")
		}
	})
	cases := []struct {
		name    string
		purpose string
		sess    uuid.UUID
		target  string
	}{
		{"other session", ticketChangesetPreview, uuid.New(), target},
		{"no session", ticketChangesetPreview, uuid.Nil, target},
		{"other changeset", ticketChangesetPreview, sess, changesetTarget(43, 1)},
		{"other seq", ticketChangesetPreview, sess, changesetTarget(42, 2)},
		{"editor preview purpose", ticketPreview, sess, target},
		{"plugin ui purpose", ticketPluginUI, sess, target},
	}
	for _, c := range cases {
		s, _ := newTestTickets()
		tok := s.mint(ticketChangesetPreview, sess, target, nil)
		if _, ok := s.consume(c.purpose, tok, c.sess, c.target); ok {
			t.Errorf("%s: accepted", c.name)
		}
		if c.sess != uuid.Nil {
			if _, ok := s.consume(ticketChangesetPreview, tok, sess, target); ok {
				t.Errorf("%s: ticket survived a wrong attempt", c.name)
			}
		}
	}
	t.Run("other purposes cannot open it", func(t *testing.T) {
		s, _ := newTestTickets()
		for _, p := range []string{ticketPreview, ticketPluginUI} {
			tok := s.mint(p, sess, target, nil)
			if _, ok := s.consume(ticketChangesetPreview, tok, sess, target); ok {
				t.Errorf("a %s ticket opened a changeset frame", p)
			}
			tok = s.mint(p, sess, target, nil)
			if _, ok := s.consumeBound(ticketChangesetPreview, tok, target); ok {
				t.Errorf("a %s ticket opened a changeset frame (bound)", p)
			}
		}
	})
	t.Run("expired", func(t *testing.T) {
		s, clock := newTestTickets()
		tok := s.mint(ticketChangesetPreview, sess, target, nil)
		clock.t = clock.t.Add(ticketTTL + time.Second)
		if _, ok := s.consume(ticketChangesetPreview, tok, sess, target); ok {
			t.Fatal("expired ticket accepted")
		}
	})
	t.Run("bound happy path and replay", func(t *testing.T) {
		s, _ := newTestTickets()
		tok := s.mint(ticketChangesetPreview, sess, target, nil)
		got, ok := s.consumeBound(ticketChangesetPreview, tok, target)
		if !ok || got != sess {
			t.Fatalf("bound = %v %v, want the minting session", got, ok)
		}
		if _, ok := s.consumeBound(ticketChangesetPreview, tok, target); ok {
			t.Fatal("bound accepted twice")
		}
	})
	t.Run("bound wrong target, expiry, forgery", func(t *testing.T) {
		s, clock := newTestTickets()
		tok := s.mint(ticketChangesetPreview, sess, target, nil)
		if _, ok := s.consumeBound(ticketChangesetPreview, tok, changesetTarget(42, 2)); ok {
			t.Error("wrong seq accepted")
		}
		if _, ok := s.consumeBound(ticketChangesetPreview, tok, target); ok {
			t.Error("ticket survived a wrong attempt")
		}
		tok = s.mint(ticketChangesetPreview, sess, target, nil)
		clock.t = clock.t.Add(ticketTTL + time.Second)
		if _, ok := s.consumeBound(ticketChangesetPreview, tok, target); ok {
			t.Error("expired accepted")
		}
		other, _ := newTestTickets()
		other.key = deriveTicketKey([]byte("other"))
		tok = s.mint(ticketChangesetPreview, sess, target, nil)
		if _, ok := other.consumeBound(ticketChangesetPreview, tok, target); ok {
			t.Error("ticket of another key accepted")
		}
		if _, ok := s.consumeBound(ticketChangesetPreview, "short", target); ok {
			t.Error("garbage accepted")
		}
	})
}

func TestParseChangesetFrame(t *testing.T) {
	tok := strings.Repeat("a", 32)
	if r, ok := parseChangesetFrame("42/frame/3/" + tok); !ok || r.id != 42 || r.seq != 3 || r.ticket != tok {
		t.Fatalf("good path: %+v %v", r, ok)
	}
	for _, bad := range []string{"042/frame/3/" + tok, "42/frame/03/" + tok, "0/frame/1/" + tok, "42/frame/0/" + tok,
		"-1/frame/1/" + tok, "42/frame/1/", "42/frame/1/" + tok + "/x", "42/edit/1/" + tok, "x/frame/1/" + tok, "42/frame/+1/" + tok} {
		if _, ok := parseChangesetFrame(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
