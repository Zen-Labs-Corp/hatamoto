// Package spam holds the site-wide spam protection settings and the checks the
// public form handlers run on every submission.
package spam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/secretbox"
)

// Captcha providers.
const (
	ProviderNone      = "none"
	ProviderTurnstile = "turnstile"
	ProviderRecaptcha = "recaptcha"
	ProviderHCaptcha  = "hcaptcha"
)

// What happens when a content rule trips.
const (
	ActionSpam   = "spam"
	ActionRefuse = "refuse"
)

// Limits on the numeric and list settings.
const (
	MaxMinSeconds   = 60
	MaxMaxLinks     = 50
	MaxBlockedWords = 200
	MaxWordLen      = 100
	MaxSiteKeyLen   = 200
)

const (
	secretDomain = "hatamoto/spam/captcha-secret"
	secretAAD    = "spam-settings:captcha-secret"
	cacheTTL     = 30 * time.Second
)

// Providers lists the valid provider values.
var Providers = []string{ProviderNone, ProviderTurnstile, ProviderRecaptcha, ProviderHCaptcha}

// Scripts lists the writing systems a site can block.
var Scripts = []string{"cyrillic", "cjk", "arabic", "hebrew", "thai", "devanagari"}

// Settings is the spam protection configuration.
type Settings struct {
	Provider       string
	SiteKey        string
	SecretSealed   []byte // sealed captcha secret; nil when none is saved
	MinSeconds     int
	MaxLinks       int
	BlockedWords   []string
	BlockedScripts []string
	Action         string
}

// Defaults is what a site gets before anyone saves the page.
func Defaults() Settings {
	return Settings{Provider: ProviderNone, MinSeconds: 3, MaxLinks: 2, Action: ActionSpam}
}

// Validate checks every field against its allowed values.
func (s Settings) Validate() error {
	if !contains(Providers, s.Provider) {
		return fmt.Errorf("unknown captcha provider %q", s.Provider)
	}
	if len(s.SiteKey) > MaxSiteKeyLen {
		return errors.New("site key is too long")
	}
	if s.MinSeconds < 0 || s.MinSeconds > MaxMinSeconds {
		return fmt.Errorf("minimum time must be between 0 and %d seconds", MaxMinSeconds)
	}
	if s.MaxLinks < 0 || s.MaxLinks > MaxMaxLinks {
		return fmt.Errorf("max links must be between 0 and %d", MaxMaxLinks)
	}
	if s.Action != ActionSpam && s.Action != ActionRefuse {
		return fmt.Errorf("unknown action %q", s.Action)
	}
	if len(s.BlockedWords) > MaxBlockedWords {
		return fmt.Errorf("at most %d blocked words", MaxBlockedWords)
	}
	for _, w := range s.BlockedWords {
		if len(w) > MaxWordLen {
			return fmt.Errorf("blocked words can be at most %d characters", MaxWordLen)
		}
	}
	for _, sc := range s.BlockedScripts {
		if !contains(Scripts, sc) {
			return fmt.Errorf("unknown script %q", sc)
		}
	}
	return nil
}

// SealSecret returns the sealed form of a captcha secret for Settings.SecretSealed.
func SealSecret(key []byte, secret string) ([]byte, error) {
	return secretbox.Seal(key, []byte(secretAAD), []byte(secret))
}

// Secret opens the stored captcha secret. ok is false when none is saved or it
// cannot be read (the server's secret changed); callers treat both as "no key".
func (s Settings) Secret(key []byte) (secret string, ok bool) {
	if len(s.SecretSealed) == 0 {
		return "", false
	}
	b, err := secretbox.Open(key, []byte(secretAAD), s.SecretSealed)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// SecretKey derives the key that seals the captcha secret from the server secret.
func SecretKey(serverSecret string) []byte { return secretbox.DeriveKey(serverSecret, secretDomain) }

// Querier is the part of a pgx pool the Store needs.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Store reads and writes the single hatamoto_spam_settings row, with a short
// cache because every public page render and form submit reads it.
type Store struct {
	db  Querier
	now func() time.Time

	mu       sync.Mutex
	cached   Settings
	cachedAt time.Time
	have     bool
}

// NewStore returns a Store over db (a *pgxpool.Pool fits).
func NewStore(db Querier) *Store { return &Store{db: db, now: time.Now} }

// Get returns the stored settings, or Defaults when no row exists. On a read
// error it serves the last good copy if it has one.
func (s *Store) Get(ctx context.Context) (Settings, error) {
	s.mu.Lock()
	if s.have && s.now().Sub(s.cachedAt) < cacheTTL {
		c := s.cached
		s.mu.Unlock()
		return c, nil
	}
	stale, haveStale := s.cached, s.have
	s.mu.Unlock()

	set, err := s.load(ctx)
	if err != nil {
		if haveStale {
			return stale, nil
		}
		return Defaults(), err
	}
	s.mu.Lock()
	s.cached, s.cachedAt, s.have = set, s.now(), true
	s.mu.Unlock()
	return set, nil
}

// Invalidate drops the cached copy so the next Get reads the database.
func (s *Store) Invalidate() {
	s.mu.Lock()
	s.have = false
	s.mu.Unlock()
}

func (s *Store) load(ctx context.Context) (Settings, error) {
	var (
		set            Settings
		words, scripts string
	)
	err := s.db.QueryRow(ctx, `
		SELECT provider, site_key, secret_sealed, min_seconds, max_links,
		       blocked_words, blocked_scripts, action
		FROM hatamoto_spam_settings WHERE id`).Scan(
		&set.Provider, &set.SiteKey, &set.SecretSealed, &set.MinSeconds, &set.MaxLinks,
		&words, &scripts, &set.Action)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), fmt.Errorf("load spam settings: %w", err)
	}
	set.BlockedWords = splitLines(words)
	set.BlockedScripts = splitCSV(scripts)
	return set, nil
}

// Save validates set and stores it, then drops the cache.
func (s *Store) Save(ctx context.Context, set Settings) error {
	if err := set.Validate(); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO hatamoto_spam_settings
			(id, provider, site_key, secret_sealed, min_seconds, max_links,
			 blocked_words, blocked_scripts, action, updated_at)
		VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (id) DO UPDATE SET provider = EXCLUDED.provider, site_key = EXCLUDED.site_key,
			secret_sealed = EXCLUDED.secret_sealed, min_seconds = EXCLUDED.min_seconds,
			max_links = EXCLUDED.max_links, blocked_words = EXCLUDED.blocked_words,
			blocked_scripts = EXCLUDED.blocked_scripts, action = EXCLUDED.action,
			updated_at = now()`,
		set.Provider, set.SiteKey, set.SecretSealed, set.MinSeconds, set.MaxLinks,
		strings.Join(set.BlockedWords, "\n"), strings.Join(set.BlockedScripts, ","), set.Action)
	if err != nil {
		return fmt.Errorf("save spam settings: %w", err)
	}
	s.Invalidate()
	return nil
}

func splitLines(v string) []string {
	var out []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
