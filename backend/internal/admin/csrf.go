package admin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// csrfHeader carries the token of htmx and fetch requests (admin.js sets it from the page meta).
const csrfHeader = "X-CSRF-Token"

// csrfToken returns the request's CSRF token ("" outside admin routes).
func csrfToken(ctx context.Context) string {
	t, _ := ctx.Value(csrfKey).(string)
	return t
}

// withoutToken is r with an empty token in its context, so the layout of a
// refusal page renders an empty csrf-token meta whatever the guard knew.
func withoutToken(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), csrfKey, ""))
}

func (a *Admin) blocked(w http.ResponseWriter, r *http.Request) {
	if a.fetchError(w, r, http.StatusForbidden, "The form expired or came from another site. Reload the page and try again.") {
		return
	}
	r = withoutToken(r)
	a.Error(w, r, http.StatusForbidden, "Request blocked",
		"The form expired or came from another site. Reload the page and try again.")
}

// streamedUploadPath (below the admin prefix) is the one multipart form whose token travels as the
// FIRST field of the body (never in the URL, which ends up in logs, history and
// Referer headers). The body is read only as far as that field, so the check
// still runs before the session check without buffering an upload for a caller
// who is not signed in; the handler reads the rest through uploadReader.
const streamedUploadPath = "plugins/upload"

// maxCSRFField bounds the token field of a streamed multipart body.
const maxCSRFField = 256

// csrfPeekBytes is how much of a multipart body is read to find the first
// part: the boundary line, the part headers and a token of at most maxCSRFField.
const csrfPeekBytes = 1024

type uploadKey struct{}

// uploadReader is the rest of a multipart body whose _csrf field was already
// read by the check, or the whole body when the token came in a header.
func uploadReader(r *http.Request) (*multipart.Reader, error) {
	if mr, ok := r.Context().Value(uploadKey{}).(*multipart.Reader); ok {
		return mr, nil
	}
	return r.MultipartReader()
}

// checkToken verifies the request's token. A multipart body is never parsed
// here (this runs before the session check, and an upload must not be buffered
// for someone who is not signed in): the token comes in the X-CSRF-Token header
// (htmx, fetch), or, on the streamed upload form, as the body's first part.
// Every other multipart form (the media picker, the import wizard) sends it the
// same way, as the first part, which is read from a bounded peek of the body and
// put back so the handler parses the whole form as usual. The token is never
// accepted from a query string, where it would reach logs, history and Referer
// headers. accept decides whether the presented token is the right one. The returned request carries the multipart reader when the first part
// was consumed by the streamed upload.
func checkToken(r *http.Request, uploadPath string, accept func(got string) bool) (*http.Request, bool) {
	got := r.Header.Get(csrfHeader)
	multipart := strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/")
	switch {
	case got != "":
	case multipart && r.URL.Path == uploadPath:
		mr, err := r.MultipartReader()
		if err != nil {
			return r, false
		}
		part, err := mr.NextPart()
		if err != nil || part.FormName() != "_csrf" || part.FileName() != "" {
			return r, false
		}
		b, err := io.ReadAll(io.LimitReader(part, maxCSRFField+1))
		if err != nil || len(b) > maxCSRFField {
			return r, false
		}
		got = string(b)
		r = r.WithContext(context.WithValue(r.Context(), uploadKey{}, mr))
	case multipart:
		got = peekFirstPartToken(r)
	default:
		got = r.PostFormValue("_csrf")
	}
	return r, accept(got)
}

// peekFirstPartToken returns the _csrf value when it is the first part of a
// multipart body, reading at most csrfPeekBytes and restoring r.Body so the
// handler still sees the complete body.
func peekFirstPartToken(r *http.Request) string {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return ""
	}
	head, _ := io.ReadAll(io.LimitReader(r.Body, csrfPeekBytes))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}

	part, err := multipart.NewReader(bytes.NewReader(head), params["boundary"]).NextPart()
	if err != nil || part.FormName() != "_csrf" || part.FileName() != "" {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(part, maxCSRFField+1))
	if len(b) > maxCSRFField || (err != nil && !errors.Is(err, io.ErrUnexpectedEOF)) {
		return ""
	}
	return string(b)
}

// sameOrigin admits an unsafe request only when the browser says it is
// same-origin. Origin "null" is what a browser sends on a same-origin form
// navigation under Referrer-Policy no-referrer, so it passes here: the
// same-origin Sec-Fetch-Site (a sandboxed or cross-site sender never has it)
// and the token still decide.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return false // missing, "none", same-site and cross-site all fail closed
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return true
}

// deriveCSRFKey derives the synchronizer-token key from the flash key under its
// own domain label, so a token is never a valid flash MAC. Without a flash key
// it falls back to a per-process random key (tokens then die on restart).
func deriveCSRFKey(flash []byte) []byte {
	if len(flash) == 0 {
		k := make([]byte, 32)
		_, _ = rand.Read(k)
		return k
	}
	m := hmac.New(sha256.New, flash)
	m.Write([]byte("hatamoto/admin/csrf"))
	return m.Sum(nil)
}

// mac is the token derivation: HMAC(csrfKey, kind ":" id), base64url.
func (a *Admin) mac(kind, id string) string {
	if a.csrfTokenFn != nil {
		return a.csrfTokenFn(kind, id)
	}
	m := hmac.New(sha256.New, a.csrfKey)
	m.Write([]byte(kind + ":" + id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// sessionToken is the token bound to a signed-in session (by session id, not
// by the bearer value, so it rotates with the session).
func (a *Admin) sessionToken(id uuid.UUID) string { return a.mac("s", id.String()) }

// preToken is the token bound to a pre-session cookie value.
func (a *Admin) preToken(cookie string) string { return a.mac("p", cookie) }

// validPreIDs are the valid hm_pre values the request carries, in the order the
// browser sent them. A page script can set an hm_pre of its own at a more
// specific Path, which browsers send first, so every cookie of the name counts
// and a bogus one cannot shadow the real one.
func (a *Admin) validPreIDs(r *http.Request) []string {
	var ids []string
	now := time.Now()
	for _, c := range r.Cookies() {
		if c.Name == preCookie && a.parsePreID(c.Value, now) {
			ids = append(ids, c.Value)
		}
	}
	return ids
}

// validPreToken reports whether got is the pre-session token of a valid hm_pre
// cookie the request carries.
func (a *Admin) validPreToken(r *http.Request, got string) bool {
	for _, id := range a.validPreIDs(r) {
		if tokenEqual(got, a.preToken(id)) {
			return true
		}
	}
	return false
}

// preIDLen is the length of the random part of a pre-session id (16 bytes, base64url).
const preIDLen = 22

// newPreID returns a pre-session cookie value "<random>.<unix expiry>".
func (a *Admin) newPreID(expires time.Time) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + strconv.FormatInt(expires.Unix(), 10)
}

// preSkew is the clock slack allowed on the far side of a pre-session expiry.
const preSkew = 5 * time.Minute

// parsePreID reports whether v is a well-formed pre-session id that has not
// expired at now and does not expire later than a cookie minted now could, so a
// pair a page script hard-codes cannot live forever.
func (a *Admin) parsePreID(v string, now time.Time) bool {
	id, exp, ok := strings.Cut(v, ".")
	if !ok || len(id) != preIDLen {
		return false
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && unix > now.Unix() && unix <= now.Add(preTTL+preSkew).Unix()
}

// tokenEqual compares tokens in constant time; an empty token never matches.
func tokenEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
