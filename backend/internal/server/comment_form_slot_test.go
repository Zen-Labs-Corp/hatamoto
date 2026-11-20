package server

import (
	"bytes"
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/spam"
)

type slotTokens struct{ used map[string]bool }

func (s *slotTokens) ConsumeFormToken(_ context.Context, id string, _ time.Time) (bool, error) {
	if s.used[id] {
		return false, nil
	}
	s.used[id] = true
	return true, nil
}

func slotFixture(on bool) (*commentSlot, comment.Keys) {
	keys := comment.NewKeys("slot-secret")
	return &commentSlot{
		tokenKey: spam.CommentTokenKey("slot-secret"), nameKey: spam.FieldNameKey("slot-secret"),
		hashIP: keys.HashIP, now: time.Now, on: func() bool { return on },
	}, keys
}

const slotForm = `<form class="f" method="post" action="/-/comments/%s" data-hm-cform hx-post="/-/comments/%s">` +
	`<input type="hidden" name="hm_ct" value="__hmcf_tok__"><textarea id="__hmcf_body__" name="__hmcf_body__"></textarea>` +
	`<input name="__hmcf_name__"><input name="__hmcf_email__"><input name="__hmcf_hp__"></form>`

func slotPage(id uuid.UUID) []byte {
	form := bytes.ReplaceAll([]byte(slotForm), []byte("%s"), []byte(id.String()))
	return append(append([]byte(`<ul><li>&lt;input value=__hmcf_tok__ data-hm-cform&gt;</li></ul><form action="/x"><p>__hmcf_tok__</p></form>`), form...), []byte("<p>after</p>")...)
}

var slotTokRe = regexp.MustCompile(`name="hm_ct" value="([^"]+)"`)

func TestCommentSlotFillPersonalises(t *testing.T) {
	slot, keys := slotFixture(true)
	id := uuid.New()
	page := slotPage(id)
	orig := append([]byte(nil), page...)
	r := httptest.NewRequest("GET", "/p", nil)
	r.RemoteAddr = "203.0.113.9:1234"

	a, ok := slot.fill(r, page)
	if !ok {
		t.Fatal("not personalised")
	}
	b, _ := slot.fill(r, page)
	if !bytes.Equal(page, orig) {
		t.Fatal("input mutated")
	}
	if bytes.Equal(a, b) {
		t.Fatal("two fills identical")
	}
	// Only the form span changed: the placeholder text outside it stays.
	if n := bytes.Count(a, []byte("__hmcf_")); n != 2 {
		t.Fatalf("placeholders left = %d, want the 2 outside the span", n)
	}
	if bytes.Contains(a[bytes.Index(a, []byte(`<form class`)):], []byte("__hmcf_")) {
		t.Fatal("placeholder left in the form")
	}
	if !bytes.HasSuffix(a, []byte("<p>after</p>")) {
		t.Fatal("tail lost")
	}
	ta, tb := slotTokRe.FindSubmatch(a)[1], slotTokRe.FindSubmatch(b)[1]
	if bytes.Equal(ta, tb) {
		t.Fatal("tokens equal")
	}
	names := spam.FieldNames(slot.nameKey, string(ta))
	if !bytes.Contains(a, []byte(`name="`+names.Body+`"`)) || !bytes.Contains(a, []byte(`name="`+names.Honeypot+`"`)) {
		t.Fatal("names do not derive from the token")
	}

	ipHash, _ := keys.HashIP("203.0.113.9")
	otherHash, _ := keys.HashIP("198.51.100.1")
	store := &slotTokens{used: map[string]bool{}}
	now := time.Now().Add(time.Minute)
	subj := spam.CommentSubject(id.String())
	if _, res := spam.CheckCommentToken(context.Background(), slot.tokenKey, store, subj, otherHash, string(ta), now); res == spam.TokenOK {
		t.Fatal("token verified for another ip")
	}
	if _, res := spam.CheckCommentToken(context.Background(), slot.tokenKey, store, subj, ipHash, string(ta), now); res != spam.TokenOK {
		t.Fatalf("token for the same ip = %v", res)
	}
}

func TestCommentSlotFillUnchanged(t *testing.T) {
	id := uuid.New()
	r := httptest.NewRequest("GET", "/p", nil)

	slot, _ := slotFixture(true)
	plain := []byte("<html><body>no form here</body></html>")
	if out, ok := slot.fill(r, plain); ok || &out[0] != &plain[0] {
		t.Fatal("marker absent: want the same slice and false")
	}

	page := slotPage(id)

	var nilSlot *commentSlot
	if out, ok := nilSlot.fill(r, page); ok || &out[0] != &page[0] {
		t.Fatal("nil slot: want the same slice and false")
	}

	bad := bytes.ReplaceAll(page, []byte(id.String()), []byte("not-a-uuid"))
	if out, ok := slot.fill(r, bad); !ok || &out[0] != &bad[0] {
		t.Fatal("bad entity id: want the same slice and private")
	}

	allocs := testing.AllocsPerRun(50, func() { slot.fill(r, plain) })
	if allocs > 1 {
		t.Fatalf("allocs without marker = %v", allocs)
	}
}

// The marker decides, not the site switch: with the flag reading off a page that
// carries the form is still filled, and sent private with no validators, on the
// miss and on the hit.
func TestCommentSlotFillsWhateverTheFlagSays(t *testing.T) {
	off, _ := slotFixture(false)
	page := slotPage(uuid.New())
	out, ok := off.fill(httptest.NewRequest("GET", "/p", nil), page)
	if !ok || !slotTokRe.Match(out) {
		t.Fatalf("flag off: private %v, form not filled in %s", ok, out)
	}

	f := newBypassFixture(t)
	slot, _ := slotFixture(false)
	f.h.slot = slot
	for i, name := range []string{"miss", "hit"} {
		w := f.get("/go")
		if w.Header().Get("ETag") != "" || !strings.Contains(w.Header().Get("Cache-Control"), "private") ||
			strings.Contains(w.Body.String(), "__hmcf_") {
			t.Fatalf("%s %d: ETag %q, Cache-Control %q, body %s", name, i, w.Header().Get("ETag"), w.Header().Get("Cache-Control"), w.Body)
		}
	}
}
