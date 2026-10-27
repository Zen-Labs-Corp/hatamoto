package admin

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
)

// confirmData drives pages/confirm: the no-JS version of the confirmation
// dialog. The POST form on it is the same one the dialog submits.
type confirmData struct {
	Title, Message, Post, Label, Back string
	Danger                            bool
	// Hidden are extra fields the confirmation form posts (selected ids).
	Hidden []hiddenField
}

type hiddenField struct{ Name, Value string }

func (a *Admin) renderConfirm(w http.ResponseWriter, r *http.Request, d confirmData) {
	v := a.NewView(w, r, d.Title)
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "confirm", v)
}

// secretStore keeps one-time secrets (a new API key) server-side for two
// minutes. The POST that mints a key redirects to a GET that takes the secret
// out, so reloading the page re-reads nothing and mints nothing: a reload of a
// POST response would otherwise create a second key, or rotate the key just
// shown. In memory on purpose: a secret must not outlive the process.
type secretStore struct {
	mu sync.Mutex
	m  map[string]secretEntry
}

type secretEntry struct {
	user, name, key string
	// kind names what the secret is ("apikey", "invite"); note is the line shown under the banner heading.
	kind, note string
	expires    time.Time
}

// banner turns the entry into the hm/secretbanner data. dismiss is the list URL without the secret.
func (e secretEntry) banner(heading, dismiss string) ui.Secret {
	return ui.Secret{Heading: heading, Note: e.note, Value: e.key, DismissHref: dismiss}
}

const secretTTL = 2 * time.Minute

// put stores an API key secret for user and returns its single-use id.
func (s *secretStore) put(user, name, key string) string {
	return s.putEntry(secretEntry{user: user, name: name, key: key, kind: "apikey"})
}

// putEntry stores e (its expiry is set here) and returns its single-use id.
func (s *secretStore) putEntry(e secretEntry) string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	id := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]secretEntry{}
	}
	now := time.Now()
	for k, e := range s.m {
		if now.After(e.expires) {
			delete(s.m, k)
		}
	}
	e.expires = now.Add(secretTTL)
	s.m[id] = e
	return id
}

// take returns and deletes the secret; only the user it was minted for can.
func (s *secretStore) take(user, id string) (secretEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	if !ok || e.user != user || time.Now().After(e.expires) {
		return secretEntry{}, false
	}
	delete(s.m, id)
	return e, true
}
