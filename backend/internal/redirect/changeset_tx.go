package redirect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Errors a changeset op can be refused with (the op is wrong, not stale).
var (
	ErrInvalidSourcePath = errors.New("redirect source_path must be a path starting with /")
	ErrInvalidTarget     = errors.New("redirect target is not a safe location")
	ErrNoRedirectID      = errors.New("redirect update or delete needs an id")
)

// Conflict reasons reported in Result.Conflict. A conflicting op changes
// nothing; the caller lists it and carries on with its other ops.
const (
	ConflictSourceTaken = "source_taken" // another redirect owns this exact source_path
	ConflictMoved       = "moved"        // the live row changed since the base token was taken
	ConflictGone        = "gone"         // the live row no longer exists
	ConflictSourceLive  = "source_live"  // the source path is a live page: a redirect would shadow it
)

// RedirectOp is one create, update or delete of a redirect inside a changeset.
// ID nil creates. On update an empty field or a zero Status keeps the stored
// value; Origin only applies to a create (an update keeps the row's owner).
type RedirectOp struct {
	ID            *uuid.UUID
	SourcePath    string
	Target        string
	Status        int
	BaseUpdatedAt *time.Time
	Origin        string
	Delete        bool
	// SameHostOnly refuses an absolute target (the public demo rule).
	SameHostOnly bool
	// SourceLive reports whether a path is served by a live page. A source of
	// any origin but the owner must be on a path nothing serves; nil skips that
	// check (not the home-page and wildcard refusal, see SourceShadows).
	SourceLive func(ctx context.Context, path string) (bool, error)
}

// ownerOrigin is the origin of the site owner's own writes.
const ownerOrigin = "owner"

func isOwner(origin string) bool { return origin == "" || origin == ownerOrigin }

// SourceShadows reports whether a redirect written by origin at source would
// shadow something the owner serves. Redirects are matched before pages, so a
// non-owner origin may not take the home page, may not use a wildcard source
// (its literal prefix could cover any number of live pages) and may not take a
// path live reports as served. live nil skips only that last check.
func SourceShadows(ctx context.Context, origin, source string, live func(ctx context.Context, path string) (bool, error)) (bool, error) {
	if isOwner(origin) {
		return false, nil
	}
	if strings.Trim(source, "/") == "" || IsPatternSource(source) {
		return true, nil
	}
	if live == nil {
		return false, nil
	}
	return live(ctx, source)
}

func (op RedirectOp) sourceLive(ctx context.Context, source string) (bool, error) {
	return SourceShadows(ctx, op.Origin, source, op.SourceLive)
}

// Preimage is a redirect row as stored, enough to put it back.
type Preimage struct {
	ID         uuid.UUID
	SourcePath string
	TargetURL  string
	StatusCode int
	IsActive   bool
	Origin     string
	UpdatedAt  time.Time
}

// Result is the outcome of one ApplyChangesetTx call.
type Result struct {
	ID uuid.UUID
	// Conflict is non-empty when the op was skipped, see the Conflict* consts.
	Conflict string
	// Before is the row as it was (nil for a create or a conflict).
	Before *Preimage
	// UpdatedAt is the new base token of a created or updated row.
	UpdatedAt time.Time
}

// ApplyChangesetTx applies op inside the caller's transaction. It neither
// commits nor invalidates the resolver cache: after the commit the caller
// must call Service.InvalidateCache. Every write runs in a savepoint, so a
// duplicate source_path leaves tx usable. A duplicate source is never
// suffixed (redirect sources are exact paths): it is a conflict.
func ApplyChangesetTx(ctx context.Context, tx pgx.Tx, op RedirectOp) (Result, error) {
	if op.Delete {
		return deleteChangesetTx(ctx, tx, op)
	}
	if op.Status != 0 && !ValidStatusCode(op.Status) {
		return Result{}, ErrInvalidStatusCode
	}
	if op.ID == nil {
		return createChangesetTx(ctx, tx, op)
	}
	return updateChangesetTx(ctx, tx, op)
}

// checkOp validates a redirect. Only the owner may point it off-site: a
// plugin's redirect target must be a path of this site.
func checkOp(source, target, origin string, sameHostOnly bool) error {
	if !strings.HasPrefix(source, "/") {
		return ErrInvalidSourcePath
	}
	if err := ValidatePatternRule(source, target); err != nil {
		return err
	}
	if !SafeLocation(target, !sameHostOnly && isOwner(origin)) {
		return ErrInvalidTarget
	}
	return nil
}

func createChangesetTx(ctx context.Context, tx pgx.Tx, op RedirectOp) (Result, error) {
	status := op.Status
	if status == 0 {
		status = 301
	}
	if err := checkOp(op.SourcePath, op.Target, op.Origin, op.SameHostOnly); err != nil {
		return Result{}, err
	}
	origin := op.Origin
	if origin == "" {
		origin = "owner"
	}
	taken, err := sourceTaken(ctx, tx, op.SourcePath, nil)
	if err != nil {
		return Result{}, err
	}
	if taken {
		return Result{Conflict: ConflictSourceTaken}, nil
	}
	if live, err := op.sourceLive(ctx, op.SourcePath); err != nil {
		return Result{}, err
	} else if live {
		return Result{Conflict: ConflictSourceLive}, nil
	}
	var res Result
	err = inSavepoint(ctx, tx, func(sp pgx.Tx) error {
		return sp.QueryRow(ctx, `
			INSERT INTO hatamoto_redirects (source_path, target_url, status_code, origin)
			VALUES ($1, $2, $3, $4)
			RETURNING id, updated_at`, op.SourcePath, op.Target, status, origin).Scan(&res.ID, &res.UpdatedAt)
	})
	if isUniqueViolation(err) { // lost a race with a concurrent writer
		return Result{Conflict: ConflictSourceTaken}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("failed to create redirect: %w", err)
	}
	return res, nil
}

func updateChangesetTx(ctx context.Context, tx pgx.Tx, op RedirectOp) (Result, error) {
	before, conflict, err := lockForChange(ctx, tx, op)
	if err != nil || conflict != "" {
		return Result{Conflict: conflict}, err
	}
	source, target, status := before.SourcePath, before.TargetURL, before.StatusCode
	if op.SourcePath != "" {
		source = op.SourcePath
	}
	if op.Target != "" {
		target = op.Target
	}
	if op.Status != 0 {
		status = op.Status
	}
	if err := checkOp(source, target, op.Origin, op.SameHostOnly); err != nil {
		return Result{}, err
	}
	if source != before.SourcePath {
		taken, err := sourceTaken(ctx, tx, source, op.ID)
		if err != nil {
			return Result{}, err
		}
		if taken {
			return Result{Conflict: ConflictSourceTaken}, nil
		}
		if live, err := op.sourceLive(ctx, source); err != nil {
			return Result{}, err
		} else if live {
			return Result{Conflict: ConflictSourceLive}, nil
		}
	}
	res := Result{ID: before.ID, Before: before}
	err = inSavepoint(ctx, tx, func(sp pgx.Tx) error {
		return sp.QueryRow(ctx, `
			UPDATE hatamoto_redirects
			SET source_path = $2, target_url = $3, status_code = $4, updated_at = now()
			WHERE id = $1
			RETURNING updated_at`, before.ID, source, target, status).Scan(&res.UpdatedAt)
	})
	if isUniqueViolation(err) {
		return Result{Conflict: ConflictSourceTaken}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("failed to update redirect: %w", err)
	}
	return res, nil
}

func deleteChangesetTx(ctx context.Context, tx pgx.Tx, op RedirectOp) (Result, error) {
	before, conflict, err := lockForChange(ctx, tx, op)
	if err != nil || conflict != "" {
		return Result{Conflict: conflict}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_redirects WHERE id = $1`, before.ID); err != nil {
		return Result{}, fmt.Errorf("failed to delete redirect: %w", err)
	}
	return Result{ID: before.ID, Before: before}, nil
}

// RestoreChangesetTx puts a redirect back as it was, for reverting an update
// or a delete: the row with the pre-image's id is overwritten, or recreated if
// it is gone. A source_path another redirect took meanwhile is a conflict.
func RestoreChangesetTx(ctx context.Context, tx pgx.Tx, p Preimage) (Result, error) {
	taken, err := sourceTaken(ctx, tx, p.SourcePath, &p.ID)
	if err != nil {
		return Result{}, err
	}
	if taken {
		return Result{Conflict: ConflictSourceTaken}, nil
	}
	res := Result{ID: p.ID}
	err = inSavepoint(ctx, tx, func(sp pgx.Tx) error {
		return sp.QueryRow(ctx, `
			INSERT INTO hatamoto_redirects (id, source_path, target_url, status_code, is_active, origin)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO UPDATE
			SET source_path = EXCLUDED.source_path, target_url = EXCLUDED.target_url,
			    status_code = EXCLUDED.status_code, is_active = EXCLUDED.is_active,
			    origin = EXCLUDED.origin, updated_at = now()
			RETURNING updated_at`, p.ID, p.SourcePath, p.TargetURL, p.StatusCode, p.IsActive, p.Origin).Scan(&res.UpdatedAt)
	})
	if isUniqueViolation(err) {
		return Result{Conflict: ConflictSourceTaken}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("failed to restore redirect: %w", err)
	}
	return res, nil
}

// lockForChange reads and row-locks the op's target and compares its base token.
func lockForChange(ctx context.Context, tx pgx.Tx, op RedirectOp) (*Preimage, string, error) {
	if op.ID == nil {
		return nil, "", ErrNoRedirectID
	}
	var p Preimage
	err := tx.QueryRow(ctx, `
		SELECT id, source_path, target_url, status_code, is_active, origin, updated_at
		FROM hatamoto_redirects WHERE id = $1 FOR UPDATE`, *op.ID).
		Scan(&p.ID, &p.SourcePath, &p.TargetURL, &p.StatusCode, &p.IsActive, &p.Origin, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ConflictGone, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to read redirect: %w", err)
	}
	// Postgres keeps microseconds; the token was read back from it.
	if op.BaseUpdatedAt != nil && !op.BaseUpdatedAt.Truncate(time.Microsecond).Equal(p.UpdatedAt.Truncate(time.Microsecond)) {
		return nil, ConflictMoved, nil
	}
	return &p, "", nil
}

// sourceTaken reports whether a redirect other than except has this source_path.
func sourceTaken(ctx context.Context, tx pgx.Tx, source string, except *uuid.UUID) (bool, error) {
	var taken bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM hatamoto_redirects WHERE source_path = $1 AND ($2::uuid IS NULL OR id <> $2))`,
		source, except).Scan(&taken)
	if err != nil {
		return false, fmt.Errorf("failed to check redirect source: %w", err)
	}
	return taken, nil
}

// inSavepoint runs fn in a nested transaction, so a failing statement does not
// poison the caller's transaction.
func inSavepoint(ctx context.Context, tx pgx.Tx, fn func(pgx.Tx) error) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(sp); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	return sp.Commit(ctx)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
