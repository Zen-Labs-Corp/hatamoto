package redirect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/database"
)

const (
	logChannelSize = 1000
	hitChannelSize = 4096
	hitFlushEvery  = 2 * time.Second
	cacheTTL       = 60 * time.Second
)

// logEntry is an internal type for the async 404 logger.
type logEntry struct {
	Path      string
	Referrer  string
	UserAgent string
}

// tenantCache holds the cached redirects. Pre-Path-B this was keyed per
// tenant; post-Path-B there is one singleton snapshot.
type tenantCache struct {
	redirects map[string]*Redirect // source_path → redirect (exact rules)
	patterns  []patternRule        // wildcard rules, most specific first, pre-split
	loadedAt  time.Time
}

// Store is the persistence the service needs; *Repository implements it.
type Store interface {
	Upsert404Entry(ctx context.Context, path, referrer, userAgent string) error
	List404Entries(ctx context.Context, params NotFoundListParams) ([]*NotFoundEntry, int, error)
	ListRecent404Entries(ctx context.Context, since time.Time, limit int) ([]*NotFoundEntry, error)
	Ignore404Entry(ctx context.Context, id uuid.UUID) error
	Delete404Entry(ctx context.Context, id uuid.UUID) error
	Link404ToRedirect(ctx context.Context, entryID, redirectID uuid.UUID) error
	CreateRedirect(ctx context.Context, input *CreateRedirectInput) (*Redirect, error)
	GetRedirectByID(ctx context.Context, id uuid.UUID) (*Redirect, error)
	UpdateRedirect(ctx context.Context, id uuid.UUID, input *UpdateRedirectInput) (*Redirect, error)
	DeleteRedirect(ctx context.Context, id uuid.UUID) error
	ListRedirects(ctx context.Context, params RedirectListParams) ([]*Redirect, int, error)
	GetActiveRedirects(ctx context.Context) ([]*Redirect, error)
	AddRedirectHits(ctx context.Context, id uuid.UUID, n int) error
}

// Service provides redirect and 404 log operations.
type Service struct {
	// SameHostOnly refuses redirect targets that leave this site (the public
	// demo would otherwise be an open redirector on the project domain).
	SameHostOnly bool

	repo   Store
	db     *database.DB
	logger *slog.Logger

	// Async 404 logger
	logCh chan logEntry
	done  chan struct{}

	// Async hit counter: hits are queued (dropped when full), summed per rule
	// and written in batches by one goroutine.
	hitCh   chan uuid.UUID
	hitDone chan struct{}

	// In-memory redirect cache (singleton).
	mu    sync.RWMutex
	cache *tenantCache

	// Ids of invalid DB rules already logged, so each is reported once.
	warnedMu sync.Mutex
	warned   map[uuid.UUID]struct{}
}

// NewService creates a new redirect service and starts the background logger.
func NewService(db *database.DB, logger *slog.Logger) *Service {
	s := NewServiceWithStore(NewRepository(db), logger)
	s.db = db
	return s
}

// NewServiceWithStore creates a service over any Store and starts the
// background logger.
func NewServiceWithStore(store Store, logger *slog.Logger) *Service {
	s := &Service{
		repo:    store,
		logger:  logger,
		logCh:   make(chan logEntry, logChannelSize),
		done:    make(chan struct{}),
		hitCh:   make(chan uuid.UUID, hitChannelSize),
		hitDone: make(chan struct{}),
		warned:  make(map[uuid.UUID]struct{}),
	}
	go s.processLogEntries()
	go s.processHits()
	return s
}

// Close stops the background logger goroutine.
func (s *Service) Close() {
	close(s.logCh)
	close(s.hitCh)
	<-s.done
	<-s.hitDone
}

// countHit queues one hit for a redirect rule. Never blocks: under a flood
// (scanners hitting a wildcard rule) surplus hits are dropped, as for 404s.
func (s *Service) countHit(id uuid.UUID) {
	select {
	case s.hitCh <- id:
	default:
	}
}

// processHits sums queued hits per rule and flushes them every hitFlushEvery
// and on Close, one UPDATE per rule.
func (s *Service) processHits() {
	defer close(s.hitDone)
	pending := map[uuid.UUID]int{}
	flush := func() {
		for id, n := range pending {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.repo.AddRedirectHits(ctx, id, n)
			cancel()
			delete(pending, id)
		}
	}
	tick := time.NewTicker(hitFlushEvery)
	defer tick.Stop()
	for {
		select {
		case id, ok := <-s.hitCh:
			if !ok {
				flush()
				return
			}
			pending[id]++
		case <-tick.C:
			flush()
		}
	}
}

// processLogEntries drains the log channel and writes to the DB.
func (s *Service) processLogEntries() {
	defer close(s.done)
	for entry := range s.logCh {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.repo.Upsert404Entry(ctx, entry.Path, entry.Referrer, entry.UserAgent); err != nil {
			s.logger.Error("failed to log 404", slog.String("path", entry.Path), slog.String("error", err.Error()))
		}
		cancel()
	}
}

// ── 404 Log API ──────────────────────────────────────────────────────────────

// Log404Async enqueues a 404 hit for background persistence. Never blocks.
func (s *Service) Log404Async(path, referrer, userAgent string) {
	select {
	case s.logCh <- logEntry{Path: path, Referrer: referrer, UserAgent: userAgent}:
	default:
		s.logger.Warn("404 log channel full, dropping entry", slog.String("path", path))
	}
}

// List404Entries returns paginated 404 entries.
func (s *Service) List404Entries(ctx context.Context, params NotFoundListParams) ([]*NotFoundEntry, int, error) {
	return s.repo.List404Entries(ctx, params)
}

// ListRecent404Entries returns up to limit open (not ignored, not redirected)
// entries last seen since the given time.
func (s *Service) ListRecent404Entries(ctx context.Context, since time.Time, limit int) ([]*NotFoundEntry, error) {
	return s.repo.ListRecent404Entries(ctx, since, limit)
}

// Ignore404Entry marks an entry as ignored.
func (s *Service) Ignore404Entry(ctx context.Context, id uuid.UUID) error {
	return s.repo.Ignore404Entry(ctx, id)
}

// Delete404Entry deletes a 404 entry.
func (s *Service) Delete404Entry(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete404Entry(ctx, id)
}

// ── Redirect CRUD ────────────────────────────────────────────────────────────

// CreateRedirect creates a redirect and invalidates the cache.
func (s *Service) CreateRedirect(ctx context.Context, input *CreateRedirectInput) (*Redirect, error) {
	if !ValidStatusCode(input.StatusCode) {
		return nil, ErrInvalidStatusCode
	}
	if err := ValidatePatternRule(input.SourcePath, input.TargetURL); err != nil {
		return nil, err
	}
	if err := s.checkTarget(input.TargetURL); err != nil {
		return nil, err
	}

	rd, err := s.repo.CreateRedirect(ctx, input)
	if err != nil {
		return nil, err
	}

	// Link 404 entry if provided
	if input.NotFoundLogID != nil {
		_ = s.repo.Link404ToRedirect(ctx, *input.NotFoundLogID, rd.ID)
	}

	s.invalidateCache()
	return rd, nil
}

// GetRedirect returns a redirect by ID.
func (s *Service) GetRedirect(ctx context.Context, id uuid.UUID) (*Redirect, error) {
	return s.repo.GetRedirectByID(ctx, id)
}

// UpdateRedirect updates a redirect and invalidates the cache.
func (s *Service) UpdateRedirect(ctx context.Context, id uuid.UUID, input *UpdateRedirectInput) (*Redirect, error) {
	if input.StatusCode != nil && !ValidStatusCode(*input.StatusCode) {
		return nil, ErrInvalidStatusCode
	}
	if input.SourcePath != nil || input.TargetURL != nil {
		// Validate the merged rule, so editing only the target is checked
		// against the stored source and vice versa.
		cur, err := s.repo.GetRedirectByID(ctx, id)
		if err != nil {
			return nil, err
		}
		source, target := cur.SourcePath, cur.TargetURL
		if input.SourcePath != nil {
			source = *input.SourcePath
		}
		if input.TargetURL != nil {
			target = *input.TargetURL
		}
		if err := ValidatePatternRule(source, target); err != nil {
			return nil, err
		}
	}
	if input.TargetURL != nil {
		if err := s.checkTarget(*input.TargetURL); err != nil {
			return nil, err
		}
	}

	rd, err := s.repo.UpdateRedirect(ctx, id, input)
	if err != nil {
		return nil, err
	}
	s.invalidateCache()
	return rd, nil
}

// DeleteRedirect deletes a redirect and invalidates the cache.
func (s *Service) DeleteRedirect(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteRedirect(ctx, id); err != nil {
		return err
	}
	s.invalidateCache()
	return nil
}

// ListRedirects returns paginated redirects.
func (s *Service) ListRedirects(ctx context.Context, params RedirectListParams) ([]*Redirect, int, error) {
	return s.repo.ListRedirects(ctx, params)
}

// ── Redirect Lookup (used by public handlers) ────────────────────────────────

// CheckRedirect looks up a redirect for the given path. Returns nil if
// no match. Reads from an in-memory cache with TTL.
func (s *Service) CheckRedirect(ctx context.Context, path string) *Redirect {
	tc := s.getTenantCache(ctx)
	if tc == nil {
		return nil
	}

	s.mu.RLock()
	rd := tc.redirects[path]
	s.mu.RUnlock()

	if rd != nil {
		s.countHit(rd.ID)
	}
	return rd
}

// Peek finds the redirect that would apply to path without counting a hit:
// exact match, then the trailing-slash toggle, then wildcard rules (the same
// order as the public redirect lookup endpoint).
func (s *Service) Peek(ctx context.Context, path string) *Redirect {
	tc := s.getTenantCache(ctx)
	if tc == nil {
		return nil
	}
	alt := path + "/"
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		alt = strings.TrimRight(path, "/")
	}
	s.mu.RLock()
	rd := tc.redirects[path]
	if rd == nil {
		rd = tc.redirects[alt]
	}
	s.mu.RUnlock()
	if rd == nil {
		rd = FindPattern(tc.patterns, path)
	}
	return rd
}

// CheckRedirectPattern matches path against the wildcard rules and returns
// the winner with its target expanded, or nil. Callers try exact rules first.
func (s *Service) CheckRedirectPattern(ctx context.Context, path string) *Redirect {
	tc := s.getTenantCache(ctx)
	if tc == nil {
		return nil
	}
	rd := FindPattern(tc.patterns, path)
	if rd != nil {
		s.countHit(rd.ID)
	}
	return rd
}

// getTenantCache returns cached redirects, refreshing if stale.
func (s *Service) getTenantCache(ctx context.Context) *tenantCache {
	s.mu.RLock()
	tc := s.cache
	if tc != nil && time.Since(tc.loadedAt) < cacheTTL {
		s.mu.RUnlock()
		return tc
	}
	s.mu.RUnlock()

	// Refresh
	redirects, err := s.repo.GetActiveRedirects(ctx)
	if err != nil {
		s.logger.Error("failed to load redirects for cache", slog.String("error", err.Error()))
		// Return stale cache if available
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.cache
	}

	m := make(map[string]*Redirect, len(redirects))
	var rules []*Redirect
	skipped := 0
	for _, rd := range redirects {
		if IsPatternSource(rd.SourcePath) {
			if err := ValidatePatternRule(rd.SourcePath, rd.TargetURL); err != nil {
				skipped++
				s.warnInvalidOnce(rd.ID, err)
				continue
			}
			rules = append(rules, rd)
			continue
		}
		m[rd.SourcePath] = rd
	}
	if skipped > 0 {
		s.logger.Warn("skipped invalid wildcard redirects", slog.Int("count", skipped))
	}
	sortPatterns(rules)
	patterns := make([]patternRule, len(rules))
	for i, rd := range rules {
		patterns[i] = newPatternRule(rd)
	}

	tc = &tenantCache{redirects: m, patterns: patterns, loadedAt: time.Now()}
	s.mu.Lock()
	s.cache = tc
	s.mu.Unlock()
	return tc
}

// warnInvalidOnce logs an invalid DB rule by id the first time it is seen.
// The row itself is never logged.
func (s *Service) warnInvalidOnce(id uuid.UUID, err error) {
	s.warnedMu.Lock()
	_, seen := s.warned[id]
	s.warned[id] = struct{}{}
	s.warnedMu.Unlock()
	if !seen {
		s.logger.Warn("ignoring invalid wildcard redirect", slog.String("id", id.String()), slog.String("error", err.Error()))
	}
}

// invalidateCache clears the cached redirects.
func (s *Service) invalidateCache() {
	s.mu.Lock()
	s.cache = nil
	s.mu.Unlock()
}

// InvalidateCache makes the next lookup reload the redirects, for writers that
// bypass this Service (the WordPress importer runs its own).
func (s *Service) InvalidateCache() { s.invalidateCache() }

// PathMove is a public path an entity left and the one it lives at now.
type PathMove struct{ From, To string }

// entityMoveTag marks the redirects MoveEntityPaths writes.
const entityMoveTag = "entity-move"

// MoveEntityPaths makes every old path of moved entities answer 301 with its
// new path. It also drops a rule whose source is a new path (a page moving
// back must not be redirected away from itself) and repoints rules that
// targeted an old path, so a visitor never walks a chain. Moves with equal or
// non-local paths are skipped.
func (s *Service) MoveEntityPaths(ctx context.Context, moves []PathMove) error {
	if s.db == nil || len(moves) == 0 {
		return nil
	}
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, m := range moves {
		if m.From == m.To || !strings.HasPrefix(m.From, "/") || !strings.HasPrefix(m.To, "/") || m.From == "/" {
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_redirects WHERE source_path = $1`, m.To); err != nil {
			return fmt.Errorf("failed to clear redirect at new path: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_redirects SET target_url = $2, updated_at = now() WHERE target_url = $1`, m.From, m.To); err != nil {
			return fmt.Errorf("failed to repoint redirects: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag)
			VALUES ($1, $2, 301, $3)
			ON CONFLICT (source_path) DO UPDATE
			SET target_url = EXCLUDED.target_url, status_code = 301, is_active = true, source_tag = EXCLUDED.source_tag, updated_at = now()`,
			m.From, m.To, entityMoveTag); err != nil {
			return fmt.Errorf("failed to store redirect: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit redirects: %w", err)
	}
	s.invalidateCache()
	return nil
}

// maxMoveChain bounds the walk that looks for a redirect loop.
const maxMoveChain = 10

// KeepEntityPaths is MoveEntityPaths for a bulk move such as a schema's URL
// pattern changing: it only adds. A path that already has a redirect row (of
// any owner, active or not) is skipped, nothing is deleted or repointed, and a
// move whose new path still redirects back to the old one (an owner's rule,
// directly or through a chain) is skipped, because it would loop. Automatic
// redirects that leave the new path are retired first: the page lives there. It returns how many
// redirects it wrote. Moves with equal or non-local paths are skipped.
func (s *Service) KeepEntityPaths(ctx context.Context, moves []PathMove) (int, error) {
	if s.db == nil || len(moves) == 0 {
		return 0, nil
	}
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	written := 0
	for _, m := range moves {
		if m.From == m.To || !strings.HasPrefix(m.From, "/") || !strings.HasPrefix(m.To, "/") || m.From == "/" {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_redirects WHERE source_path = $1)`, m.From).Scan(&exists); err != nil {
			return 0, fmt.Errorf("failed to look up redirect: %w", err)
		}
		if exists {
			continue
		}
		// An automatic redirect that sends the new path away is stale: the page
		// answers there now (a pattern changed back). An owner's rule stays.
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_redirects SET is_active = false, updated_at = now()
			WHERE source_path = $1 AND source_tag = $2 AND is_active`, m.To, entityMoveTag); err != nil {
			return 0, fmt.Errorf("failed to retire redirect: %w", err)
		}
		loops, err := redirectsBackTo(ctx, tx, m.To, m.From)
		if err != nil {
			return 0, err
		}
		if loops {
			continue
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag)
			VALUES ($1, $2, 301, $3)
			ON CONFLICT (source_path) DO NOTHING`, m.From, m.To, entityMoveTag)
		if err != nil {
			return 0, fmt.Errorf("failed to store redirect: %w", err)
		}
		written += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("failed to commit redirects: %w", err)
	}
	if written > 0 {
		s.invalidateCache()
	}
	return written, nil
}

// redirectsBackTo reports whether following the active redirects from start
// reaches target within maxMoveChain hops.
func redirectsBackTo(ctx context.Context, tx pgx.Tx, start, target string) (bool, error) {
	at := start
	for range maxMoveChain {
		var next string
		err := tx.QueryRow(ctx, `SELECT target_url FROM hatamoto_redirects WHERE source_path = $1 AND is_active`, at).Scan(&next)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("failed to follow redirect: %w", err)
		}
		if u, perr := url.Parse(next); perr == nil && u.Path != "" {
			next = u.Path
		}
		if next == target {
			return true, nil
		}
		at = next
	}
	return true, nil // too deep to prove it is not a loop
}

// RetireEntityMoves deactivates the automatic redirects (written by
// MoveEntityPaths) whose source is one of paths. A page that now answers at a
// path must win over the redirect an earlier rename left there: rename
// /about to /about-us, publish a new /about, and /about serves the new page.
// Redirects an owner created keep their precedence; they are never touched
// here. The row stays, inactive, as a record of the rename.
func (s *Service) RetireEntityMoves(ctx context.Context, paths []string) error {
	if s.db == nil || len(paths) == 0 {
		return nil
	}
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()
	tag, err := conn.Exec(ctx, `UPDATE hatamoto_redirects SET is_active = false, updated_at = now()
		WHERE source_path = ANY($1) AND source_tag = $2 AND is_active`, paths, entityMoveTag)
	if err != nil {
		return fmt.Errorf("failed to retire redirects: %w", err)
	}
	if tag.RowsAffected() > 0 {
		s.invalidateCache()
	}
	return nil
}

// checkTarget refuses a target that leaves this site when SameHostOnly is set:
// only a single-slash relative path is allowed.
func (s *Service) checkTarget(target string) error {
	if s.SameHostOnly && !SafeLocation(target, false) {
		return ErrExternalTarget
	}
	return nil
}
