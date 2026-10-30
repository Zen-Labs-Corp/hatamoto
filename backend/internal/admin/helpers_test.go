package admin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/person"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/user"
)

type fakeSessions struct {
	mu      sync.Mutex // requests may run in parallel
	byToken map[string]*session.Session
	revoked []uuid.UUID
}

func (f *fakeSessions) Validate(_ context.Context, token string) (*session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.byToken[token]; ok {
		// Like the real store: hand back the state as it was, then bump
		// last_used_at.
		out := *s
		now := time.Now()
		s.LastUsedAt = &now
		return &out, nil
	}
	return nil, session.ErrInvalid
}
func (f *fakeSessions) Revoke(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, id)
	for tok, s := range f.byToken {
		if s.ID == id {
			delete(f.byToken, tok)
		}
	}
	return nil
}
func (f *fakeSessions) RevokeAllForUser(context.Context, uuid.UUID) error { return nil }

type fakeUsers map[uuid.UUID]*user.User

func (f fakeUsers) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return nil, user.ErrUserNotFound
}

type fakeSite struct {
	mu  sync.Mutex
	cfg siteconfig.Config
}

func newFakeSite(name string) *fakeSite {
	done := time.Now()
	return &fakeSite{cfg: siteconfig.Config{CompanyName: name, Language: "en", SetupCompletedAt: &done}}
}

func (f *fakeSite) Get(context.Context) (*siteconfig.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.cfg
	return &c, nil
}

// SetLocaleProfile merges one locale into the stored map like the SQL does.
func (f *fakeSite) SetLocaleProfile(_ context.Context, tag string, raw json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(f.cfg.LocaleProfiles, &m)
	if len(raw) == 0 {
		delete(m, tag)
	} else {
		m[tag] = raw
	}
	f.cfg.LocaleProfiles, _ = json.Marshal(m)
	return nil
}

func (f *fakeSite) Update(_ context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.CompanyName != nil {
		f.cfg.CompanyName = *in.CompanyName
	}
	if in.Timezone != nil {
		f.cfg.Timezone = *in.Timezone
	}
	if in.Language != nil {
		f.cfg.Language = *in.Language
	}
	if in.Locales != nil {
		f.cfg.Locales = *in.Locales
	}
	if in.PrefixAll != nil {
		f.cfg.PrefixAll = *in.PrefixAll
	}
	if in.XDefaultLocale != nil {
		f.cfg.XDefaultLocale = *in.XDefaultLocale
	}
	if in.PublicBaseURL != nil {
		f.cfg.PublicBaseURL = *in.PublicBaseURL
	}
	if in.PrimaryColor != nil {
		f.cfg.PrimaryColor = *in.PrimaryColor
	}
	if in.Tagline != nil {
		f.cfg.Tagline = *in.Tagline
	}
	if in.NotificationEmail != nil {
		f.cfg.NotificationEmail = *in.NotificationEmail
	}
	if in.Phone != nil {
		f.cfg.Phone = *in.Phone
	}
	if in.BusinessHours != nil {
		f.cfg.BusinessHours = *in.BusinessHours
	}
	if len(in.PostalAddress) > 0 {
		f.cfg.PostalAddress = in.PostalAddress
	}
	if len(in.BusinessProfile) > 0 {
		f.cfg.BusinessProfile = in.BusinessProfile
	}
	if in.LogoURL != nil {
		f.cfg.LogoURL = *in.LogoURL
	}
	if in.CustomDomain != nil {
		f.cfg.CustomDomain = *in.CustomDomain
	}
	if in.PageCacheEnabled != nil {
		f.cfg.PageCacheEnabled = *in.PageCacheEnabled
	}
	if in.PageCacheTTLSeconds != nil {
		f.cfg.PageCacheTTLSeconds = *in.PageCacheTTLSeconds
	}
	if in.PageCacheMaxMB != nil {
		f.cfg.PageCacheMaxMB = *in.PageCacheMaxMB
	}
	if in.PagePreloadEnabled != nil {
		f.cfg.PagePreloadEnabled = *in.PagePreloadEnabled
	}
	if in.PagePreloadIntervalSeconds != nil {
		f.cfg.PagePreloadIntervalSeconds = *in.PagePreloadIntervalSeconds
	}
	if in.SetupCompleted && f.cfg.SetupCompletedAt == nil {
		now := time.Now()
		f.cfg.SetupCompletedAt = &now
	}
	c := f.cfg
	return &c, nil
}

// fakePeople is an in-memory PeopleStore: a person per user, made on demand.
type fakePeople struct {
	mu     sync.Mutex
	byUser map[uuid.UUID]*person.Person
	// loose are people without a login; posts are their post counts.
	loose  []*person.Person
	posts  map[uuid.UUID]int
	merged [][2]uuid.UUID
}

func newFakePeople() *fakePeople {
	return &fakePeople{byUser: map[uuid.UUID]*person.Person{}, posts: map[uuid.UUID]int{}}
}

func (f *fakePeople) List(context.Context) ([]*person.Person, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]*person.Person{}, f.loose...)
	for _, p := range f.byUser {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakePeople) PostCounts(context.Context) (map[uuid.UUID]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID]int{}
	for k, v := range f.posts {
		out[k] = v
	}
	return out, nil
}

func (f *fakePeople) LinkedUsers(context.Context) (map[uuid.UUID]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID]uuid.UUID{}
	for u, p := range f.byUser {
		out[p.ID] = u
	}
	return out, nil
}

func (f *fakePeople) MergePeople(_ context.Context, keepID, dropID uuid.UUID) (*person.MergeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep, drop *person.Person
	var dropUser *uuid.UUID
	keepLinked := false
	for u, p := range f.byUser {
		u := u
		switch p.ID {
		case keepID:
			keep, keepLinked = p, true
		case dropID:
			drop, dropUser = p, &u
		}
	}
	for _, p := range f.loose {
		switch p.ID {
		case keepID:
			keep = p
		case dropID:
			drop = p
		}
	}
	if keep == nil || drop == nil {
		return nil, person.ErrNotFound
	}
	if keepLinked && dropUser != nil {
		return nil, person.ErrMergeBothLogins
	}
	n := f.posts[dropID]
	f.posts[keepID] += n
	delete(f.posts, dropID)
	if dropUser != nil {
		f.byUser[*dropUser] = keep
	} else {
		for i, p := range f.loose {
			if p.ID == dropID {
				f.loose = append(f.loose[:i], f.loose[i+1:]...)
				break
			}
		}
	}
	f.merged = append(f.merged, [2]uuid.UUID{keepID, dropID})
	return &person.MergeResult{Keep: keep, Drop: drop, Posts: n}, nil
}

func (f *fakePeople) Create(_ context.Context, _ *uuid.UUID, name string) (*person.Person, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &person.ValidationError{Fields: map[string]string{"name": "Enter a name"}}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &person.Person{ID: uuid.New(), Slug: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, Links: map[string]string{}}
	f.loose = append(f.loose, p)
	return p, nil
}

// set stores a ready person for a user.
func (f *fakePeople) set(userID uuid.UUID, p *person.Person) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byUser[userID] = p
}

func (f *fakePeople) ForUser(_ context.Context, id uuid.UUID) (*person.Person, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.byUser[id]; ok {
		c := *p
		return &c, nil
	}
	return nil, person.ErrNotFound
}

func (f *fakePeople) Save(ctx context.Context, id uuid.UUID, in person.ProfileInput) (*person.Person, error) {
	if verr := in.Validate(); verr != nil {
		return nil, verr
	}
	cur, err := f.ForUser(ctx, id)
	if err != nil {
		cur = &person.Person{ID: uuid.New(), Slug: strings.ToLower(strings.ReplaceAll(in.Name, " ", "-")), Links: map[string]string{}}
	}
	cur.Name, cur.Photo, cur.JobTitle, cur.ShortBio, cur.Public = in.Name, in.Photo, in.JobTitle, in.ShortBio, in.Public
	cur.Links = map[string]string{}
	if in.LinkedIn != "" {
		cur.Links["linkedin"] = in.LinkedIn
	}
	f.set(id, cur)
	return cur, nil
}

// rig is an admin mounted on a mux with a signed-in owner and an editor.
type rig struct {
	t        *testing.T
	a        *Admin
	h        http.Handler
	sessions *fakeSessions
	owner    *user.User
	editor   *user.User
}

func newRig(t *testing.T, mutate ...func(*Deps)) *rig {
	t.Helper()
	name := "Owner Olga"
	owner := &user.User{ID: uuid.New(), Email: "owner@example.com", Role: user.RoleOwner, Name: &name}
	editor := &user.User{ID: uuid.New(), Email: "ed@example.com", Role: user.RoleEditor}
	now := time.Now()
	sessions := &fakeSessions{byToken: map[string]*session.Session{
		"owner-token":  {ID: uuid.New(), Kind: session.KindAdmin, UserID: &owner.ID, ExpiresAt: now.Add(time.Hour), LastUsedAt: &now},
		"editor-token": {ID: uuid.New(), Kind: session.KindAdmin, UserID: &editor.ID, ExpiresAt: now.Add(time.Hour)},
		"idle-token":   {ID: uuid.New(), Kind: session.KindAdmin, UserID: &owner.ID, ExpiresAt: now.Add(time.Hour), LastUsedAt: ptr(now.Add(-8 * 24 * time.Hour))},
	}}
	d := Deps{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sessions: sessions,
		Users:    fakeUsers{owner.ID: owner, editor.ID: editor},
		Site:     newFakeSite("Acme Site"),
		FlashKey: []byte("0123456789abcdef0123456789abcdef"),
		BaseURL:  func(*http.Request) string { return "https://acme.example" },
	}
	for _, m := range mutate {
		m(&d)
	}
	a, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	a.csrfTokenFn = func(_, _ string) string { return csrfValue } // keeps rendered tokens and goldens stable
	mux := http.NewServeMux()
	a.Mount(httpx.NewGroup(mux, ""))
	return &rig{t: t, a: a, h: mux, sessions: sessions, owner: owner, editor: editor}
}

// tokenFor is the real synchronizer token of the session behind sessionToken.
func (g *rig) tokenFor(sessionToken string) string {
	return g.a.sessionToken(g.sessions.byToken[sessionToken].ID)
}

// realTokens switches this rig from the constant seam token to real derivation.
func (g *rig) realTokens() { g.a.csrfTokenFn = nil }

func ptr[T any](v T) *T { return &v }

const csrfValue = "csrf-token-csrf-token-csrf-token-0123456789"

// do issues a request as a browser would: session cookie, Sec-Fetch-* metadata
// and the synchronizer token (see addFetchMeta); hdr overrides apply last and an
// empty value deletes the header.
func (g *rig) do(method, target, token string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	g.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	}
	g.addFetchMeta(req, token, hdr)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}
