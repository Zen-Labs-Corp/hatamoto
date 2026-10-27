package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// A frame ticket is the credential in the URL of an admin document that loads
// in a frame (the builder canvas, the blog preview, a plugin page). It is
// one-time, lives 60 seconds, and is bound to the signed-in session that the
// minting page belonged to and to its target (an entity, or a plugin file).
//
// Wire form: 8 random bytes of nonce, then the first 16 bytes of an
// HMAC-SHA256 over (purpose, session, target, nonce, expiry), base64url without
// padding: exactly 32 characters of [A-Za-z0-9_-], which is the shape the
// admin-address intercept accepts for a preview URL.
const (
	ticketTTL = 60 * time.Second

	ticketPreview  = "preview"
	ticketPluginUI = "pluginui"

	ticketLen = 32
	nonceLen  = 8
	tagLen    = 16
)

const (
	ticketMax = 256
	// previewMaxBody is what the render preview accepts for one draft;
	// ticketMaxBytes bounds every staged body at once.
	previewMaxBody = 4 << 20
	ticketMaxBytes = 32 << 20
)

type issuedTicket struct {
	purpose string
	session uuid.UUID
	target  string
	expiry  time.Time
	payload []byte
}

// ticketStore is the bounded set of tickets that were minted and not yet used.
// The HMAC makes a forged string fail; the set makes a ticket one-time and
// carries the staged body of a preview.
type ticketStore struct {
	mu  sync.Mutex
	m   map[string]issuedTicket // key: the nonce, base64url
	key []byte
	now func() time.Time // nil: time.Now (tests set it)
}

// deriveTicketKey is the ticket HMAC key, domain separated from the other
// users of csrfKey. It is deliberately not a.mac: that sits behind a test seam.
func deriveTicketKey(csrfKey []byte) []byte {
	m := hmac.New(sha256.New, csrfKey)
	m.Write([]byte("hatamoto/admin/frame-ticket"))
	return m.Sum(nil)
}

func (s *ticketStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// tag is the truncated HMAC over length-prefixed fields, so one field cannot
// bleed into the next.
func (s *ticketStore) tag(purpose string, sess uuid.UUID, target string, nonce []byte, exp int64) []byte {
	m := hmac.New(sha256.New, s.key)
	field := func(b []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		m.Write(n[:])
		m.Write(b)
	}
	field([]byte(purpose))
	field(sess[:])
	field([]byte(target))
	field(nonce)
	var e [8]byte
	binary.BigEndian.PutUint64(e[:], uint64(exp))
	field(e[:])
	return m.Sum(nil)[:tagLen]
}

// mint issues a ticket for sess and target and keeps payload (may be nil) until
// the ticket is consumed or expires. It returns "" if the inputs are unusable.
func (s *ticketStore) mint(purpose string, sess uuid.UUID, target string, payload []byte) string {
	if len(s.key) == 0 || sess == uuid.Nil || strings.ContainsRune(purpose, 0) || strings.ContainsRune(target, 0) {
		return ""
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return ""
	}
	now := s.clock()
	expiry := now.Add(ticketTTL)
	raw := append(nonce[:nonceLen:nonceLen], s.tag(purpose, sess, target, nonce, expiry.Unix())...)
	key := base64.RawURLEncoding.EncodeToString(nonce)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]issuedTicket{}
	}
	total := len(payload)
	for k, v := range s.m {
		if now.After(v.expiry) {
			delete(s.m, k)
			continue
		}
		total += len(v.payload)
	}
	for len(s.m) > 0 && (len(s.m) >= ticketMax || total > ticketMaxBytes) { // evict the oldest
		var oldest string
		var at time.Time
		for k, v := range s.m {
			if oldest == "" || v.expiry.Before(at) {
				oldest, at = k, v.expiry
			}
		}
		total -= len(s.m[oldest].payload)
		delete(s.m, oldest)
	}
	s.m[key] = issuedTicket{purpose: purpose, session: sess, target: target, expiry: expiry, payload: payload}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// consume checks tok against the request's session and the target and spends
// it. Any lookup hit deletes the record, right or wrong, so a ticket cannot be
// probed. Malformed input is a miss with no state change.
func (s *ticketStore) consume(purpose, tok string, sess uuid.UUID, target string) ([]byte, bool) {
	if len(tok) != ticketLen || sess == uuid.Nil || len(s.key) == 0 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != nonceLen+tagLen {
		return nil, false
	}
	nonce, got := raw[:nonceLen], raw[nonceLen:]
	key := base64.RawURLEncoding.EncodeToString(nonce)

	s.mu.Lock()
	rec, ok := s.m[key]
	delete(s.m, key)
	s.mu.Unlock()
	if !ok || s.clock().After(rec.expiry) {
		return nil, false
	}
	if rec.purpose != purpose || rec.session != sess || rec.target != target {
		return nil, false
	}
	if !hmac.Equal(got, s.tag(rec.purpose, rec.session, rec.target, nonce, rec.expiry.Unix())) {
		return nil, false
	}
	return rec.payload, true
}

type previewPayloadKey struct{}

// previewPayload is the staged body the guard left on the request after it
// spent the preview ticket.
func previewPayload(ctx context.Context) ([]byte, bool) {
	b, ok := ctx.Value(previewPayloadKey{}).([]byte)
	return b, ok
}

// mintPreview stages body for the signed-in session of r and returns the ticket
// for the entity's preview frame URL. ok is false without a session.
func (a *Admin) mintPreview(r *http.Request, entityID uuid.UUID, body []byte) (string, bool) {
	_, sess, ok := a.cachedAuth(r)
	if !ok || sess == nil {
		return "", false
	}
	tok := a.tickets.mint(ticketPreview, sess.ID, entityID.String(), body)
	return tok, tok != ""
}
