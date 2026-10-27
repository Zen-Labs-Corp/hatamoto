package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const flashCookie = "hm_flash"

// Flash is a one-shot message carried across a redirect.
type Flash struct {
	Kind    string `json:"k"` // success | error | warning | info
	Message string `json:"m"`
	Expires int64  `json:"e"`
	Inline  bool   `json:"-"` // stay beside the form instead of becoming a toast
}

func (a *Admin) flashSig(payload string) string {
	m := hmac.New(sha256.New, a.deps.FlashKey)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SetFlash stores a message for the next page view (60 s, HMAC-signed so it
// cannot be forged into the page).
func (a *Admin) SetFlash(w http.ResponseWriter, r *http.Request, kind, message string) {
	a.setFlashAt(w, r, a.adminPath, kind, message)
}

// setFlashAt is SetFlash for a redirect to another admin address: the cookie
// is scoped to the address the next page is served at, or the browser would
// never send it there.
func (a *Admin) setFlashAt(w http.ResponseWriter, r *http.Request, path, kind, message string) {
	message = truncateFlash(message)
	b, _ := json.Marshal(Flash{Kind: kind, Message: message, Expires: time.Now().Add(time.Minute).Unix()})
	p := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: p + "." + a.flashSig(p), Path: path, MaxAge: 60,
		HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteStrictMode,
	})
}

// takeFlash reads and clears the flash cookie.
func (a *Admin) takeFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: a.adminPath, MaxAge: -1, HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteStrictMode})
	p, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(a.flashSig(p))) {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil
	}
	var f Flash
	if json.Unmarshal(raw, &f) != nil || f.Expires < time.Now().Unix() || f.Message == "" {
		return nil
	}
	switch f.Kind {
	case "success", "error", "warning", "info":
	default:
		f.Kind = "info"
	}
	return &f
}

// flashMaxRunes keeps a message well inside the 4 KB a browser stores per
// cookie: an error text carried over a redirect must not make it vanish.
const flashMaxRunes = 300

func truncateFlash(m string) string {
	if r := []rune(m); len(r) > flashMaxRunes {
		return strings.TrimSpace(string(r[:flashMaxRunes])) + "…"
	}
	return m
}
