package server

import (
	"bytes"
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/comment/identity"
	"hatamoto.dev/core/internal/commentsubmit"
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
	`<input name="__hmcf_name__"><input name="__hmcf_email__"><input name="__hmcf_hp__"><input type="checkbox" name="__hmcf_extra__"></form>`

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
	if !bytes.Contains(a, []byte(`name="`+spam.FieldNames(slot.nameKey, string(slotTokRe.FindSubmatch(a)[1])).Extra+`"`)) {
		t.Fatal("extra placeholder not filled with names.Extra")
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

	subj := spam.CommentSubject(id.String())
	now := time.Now().Add(time.Minute)
	for _, tc := range []struct {
		ip string
		ok bool
	}{
		{"198.51.100.1", false},
		{"203.0.113.9", true},
		{"203.0.113.200", true},
	} {
		_, netHash := keys.HashIP(tc.ip)
		tok := string(slotTokRe.FindSubmatch(mustFill(t, slot, r, page))[1])
		store := &slotTokens{used: map[string]bool{}}
		if _, res := spam.CheckCommentToken(context.Background(), slot.tokenKey, store, subj, netHash, tok, now); (res == spam.TokenOK) != tc.ok {
			t.Fatalf("token checked from %s = %v, want ok=%v", tc.ip, res, tc.ok)
		}
	}
}

func mustFill(t *testing.T, slot *commentSlot, r *http.Request, page []byte) []byte {
	t.Helper()
	out, ok := slot.fill(r, page)
	if !ok {
		t.Fatal("not personalised")
	}
	return out
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

func echoSlotPage(id uuid.UUID) []byte {
	page := string(slotPage(id))
	page = strings.Replace(page, `<textarea id="__hmcf_body__" name="__hmcf_body__"></textarea>`,
		`<textarea id="__hmcf_body__" name="__hmcf_body__">__hmcf_vbody__</textarea>`, 1)
	page = strings.Replace(page, `<input name="__hmcf_name__">`, `<input name="__hmcf_name__" value="__hmcf_vname__">`, 1)
	page = strings.Replace(page, `<input name="__hmcf_email__">`, `<input name="__hmcf_email__" value="__hmcf_vemail__">`, 1)
	return []byte(page)
}

func echoFill(t *testing.T, slot *commentSlot, page []byte, e *commentEcho) string {
	t.Helper()
	r := httptest.NewRequest("GET", "/p", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	if e != nil {
		r = r.WithContext(withCommentEcho(r.Context(), *e))
	}
	out := mustFill(t, slot, r, page)
	return string(out[bytes.Index(out, []byte(`<form class`)):])
}

func TestCommentSlotFillEchoValues(t *testing.T) {
	slot, _ := slotFixture(true)
	id := uuid.New()
	page := echoSlotPage(id)
	body := `a <b>"x"</b> & __hmcf_tok__ __hmcf_body__ __hmcf_vbody__`
	typed := &commentsubmit.Typed{Body: body, Name: "Ana", Email: "ana@example.test"}

	out := echoFill(t, slot, page, &commentEcho{EntityID: id, Typed: typed})
	m := regexp.MustCompile(`(?s)<textarea[^>]*>(.*?)</textarea>`).FindStringSubmatch(out)
	if m == nil || m[1] != html.EscapeString(body) {
		t.Fatalf("textarea = %q, want the escaped typed body verbatim", m)
	}
	tok := slotTokRe.FindStringSubmatch(out)
	if tok == nil || strings.Contains(tok[1], "__hmcf_") {
		t.Fatalf("hm_ct = %v, want a real token", tok)
	}
	names := spam.FieldNames(slot.nameKey, tok[1])
	if !strings.Contains(out, `name="`+names.Body+`"`) || !strings.Contains(out, `name="`+names.Name+`" value="Ana"`) ||
		!strings.Contains(out, `value="ana@example.test"`) {
		t.Fatalf("names or values wrong: %s", out)
	}

	// Another entity or no echo: the placeholders become empty.
	for _, e := range []*commentEcho{{EntityID: uuid.New(), Typed: typed}, nil} {
		out := echoFill(t, slot, page, e)
		if strings.Contains(out, "__hmcf_") || strings.Contains(out, "Ana") {
			t.Fatalf("no echo for this entity, got: %s", out)
		}
	}

	out = echoFill(t, slot, page, &commentEcho{EntityID: id, Typed: &commentsubmit.Typed{Body: "\nhi"}})
	if !strings.Contains(out, ">\n\nhi</textarea>") {
		t.Fatalf("leading newline not kept: %s", out)
	}
}

const lockedSlotForm = `<form method="post" action="/-/comments/%s" data-hm-cform><input type="hidden" name="hm_ct" value="__hmcf_tok__">` +
	`<p __hmcf_hnamed__>as <strong>__hmcf_who__</strong></p><p __hmcf_hin__>signed in</p><p __hmcf_hout__>sign in</p>` +
	`<textarea name="__hmcf_body__" __hmcf_boff__></textarea><button type="submit" __hmcf_boff__>post</button></form>`

func lockedSlotPage(id uuid.UUID) []byte {
	return []byte(`<p>__hmcf_who__ __hmcf_boff__</p>` + strings.ReplaceAll(lockedSlotForm, "%s", id.String()) + `<p>__hmcf_hin__</p>`)
}

func lockedSlotFixture(name string, calls *int) *commentSlot {
	slot, _ := slotFixture(true)
	slot.verify = func(v string, _ time.Time) (identity.DeviceClaims, bool) {
		return identity.DeviceClaims{CommenterID: uuid.New()}, v == "good"
	}
	slot.names = newSignedNames(func(context.Context, uuid.UUID, uuid.UUID) (string, error) {
		*calls++
		return name, nil
	}, time.Now)
	return slot
}

func fillLocked(t *testing.T, slot *commentSlot, cookie string) string {
	t.Helper()
	r := httptest.NewRequest("GET", "/p", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: cookie})
	}
	out, ok := slot.fill(r, lockedSlotPage(uuid.New()))
	if !ok {
		t.Fatal("not filled")
	}
	return string(out)
}

func TestCommentSlotLockedPlaceholderSwap(t *testing.T) {
	calls := 0
	signedIn := fillLocked(t, lockedSlotFixture("story_author", &calls), "good")
	for _, want := range []string{`<p >as <strong>story_author</strong></p>`, `<p hidden>signed in</p>`, `<p hidden>sign in</p>`, `<textarea name=`, `<button type="submit" >post`} {
		if !strings.Contains(signedIn, want) {
			t.Errorf("signed in lacks %q: %s", want, signedIn)
		}
	}
	if strings.Contains(signedIn, "disabled") {
		t.Error("signed in: body disabled")
	}

	calls = 0
	out := fillLocked(t, lockedSlotFixture("x", &calls), "")
	for _, want := range []string{`<p hidden>as <strong></strong></p>`, `<p hidden>signed in</p>`, `<p >sign in</p>`, `disabled></textarea>`, `<button type="submit" disabled>post`} {
		if !strings.Contains(out, want) {
			t.Errorf("signed out lacks %q: %s", want, out)
		}
	}
	if calls != 0 {
		t.Error("a signed-out view looked a name up")
	}
	// Outside the form span nothing is touched.
	if !strings.HasPrefix(out, `<p>__hmcf_who__ __hmcf_boff__</p>`) || !strings.HasSuffix(out, `<p>__hmcf_hin__</p>`) {
		t.Errorf("text outside the span was rewritten: %s", out)
	}
}

// With no script at all the per-request fill alone leaves exactly one visible
// state line, and the body and submit enabled only for a valid device.
func TestCommentSlotLockedNoScriptSingleLine(t *testing.T) {
	for _, tc := range []struct {
		cookie   string
		name     string
		visible  string
		disabled int
	}{{"good", "story_author", "as <strong>story_author", 0}, {"good", "", "signed in", 0}, {"", "x", "sign in", 2}, {"bad", "x", "sign in", 2}} {
		calls := 0
		out := fillLocked(t, lockedSlotFixture(tc.name, &calls), tc.cookie)
		if n := strings.Count(out, "<p >"); n != 1 || !strings.Contains(out, "<p >"+tc.visible) {
			t.Errorf("cookie %q name %q: want one visible line %q: %s", tc.cookie, tc.name, tc.visible, out)
		}
		if n := strings.Count(out, "<p hidden>"); n != 2 {
			t.Errorf("cookie %q: %d hidden lines, want 2: %s", tc.cookie, n, out)
		}
		if n := strings.Count(out, "disabled"); n != tc.disabled {
			t.Errorf("cookie %q: %d disabled controls, want %d: %s", tc.cookie, n, tc.disabled, out)
		}
		if f := out[strings.Index(out, "<form"):strings.Index(out, "</form>")]; strings.Contains(f, "__hmcf_") {
			t.Errorf("cookie %q: placeholder left: %s", tc.cookie, out)
		}
	}
}

func TestCommentSlotLockedNameIsEscapedAndNotRewritten(t *testing.T) {
	calls := 0
	hostile := `<script>alert(1)</script> __hmcf_tok__ __hmcf_hin__`
	out := fillLocked(t, lockedSlotFixture(hostile, &calls), "good")
	if strings.Contains(out, "<script>") {
		t.Errorf("name not escaped: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt; __hmcf_tok__ __hmcf_hin__") {
		t.Errorf("name rewritten in a second pass: %s", out)
	}
}

func TestCommentSlotUnlockedSpanNeverLooksUpAName(t *testing.T) {
	calls := 0
	slot := lockedSlotFixture("story_author", &calls)
	r := httptest.NewRequest("GET", "/p", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	r.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "good"})
	if _, ok := slot.fill(r, slotPage(uuid.New())); !ok || calls != 0 {
		t.Errorf("unlocked span: ok=%v lookups=%d", ok, calls)
	}
}
