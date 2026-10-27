package v2

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
)

// InsertInput is the per-call input the /linking/insert endpoint and the
// suggestion-panel "Insert" action push through Insert(). The caller has
// already resolved CanonicalURL via Resolve() — Insert is deliberately
// agnostic to schema so it can wrap any pre-resolved URL.
//
// TargetSlug + TargetSchemaSlug populate the generated `<a>` tag's
// `data-target-slug` / `data-target-schema` attributes. The handler reads
// these off the target entity row it already loaded, so no extra DB hits.
type InsertInput struct {
	SourceEntityID   uuid.UUID
	TargetEntityID   uuid.UUID
	TargetSlug       string
	TargetSchemaSlug string
	AnchorText       string
	CanonicalURL     string
}

// InsertResult reports what Insert did. Inserted is true exactly when stored
// content changed; SkipReason is set otherwise (mirrors the WrapAll skip
// taxonomy).
type InsertResult struct {
	Inserted    bool
	SkipReason  string
	NewContent  string
	ContentSize int
}

// ErrSourceNotFound signals the source entity row is missing. The /insert
// handler maps this to 404 so the editor knows to refresh.
var ErrSourceNotFound = errors.New("source entity not found")

// Insert wraps a single anchor span in a source entity's stored content with
// an <a href data-entity-id class="entity-link"> element and refreshes the
// reverse-lookup index in the same transaction.
//
// Concurrency: takes SELECT ... FOR UPDATE on hatamoto_entities for the source
// row, mirroring rewriter.go's strategy. This serialises Insert against the
// rewriter and any concurrent editor save touching the same row.
//
// Idempotent: WrapAll's hasEntityLink guard reports SkipAlreadyLinked when
// the source already contains an anchor to TargetEntityID, so re-firing
// Insert with the same input is a no-op (no second wrap, no DB write).
//
// On skip (no_match, in_heading, etc.) the function returns success with
// Inserted=false and SkipReason set — the caller (UI) decides whether to
// surface that to the user. No transaction is committed when nothing changes.
func Insert(ctx context.Context, db *database.DB, in InsertInput) (InsertResult, error) {
	if in.SourceEntityID == uuid.Nil {
		return InsertResult{}, fmt.Errorf("source entity id is required")
	}
	if in.TargetEntityID == uuid.Nil {
		return InsertResult{}, fmt.Errorf("target entity id is required")
	}
	if in.CanonicalURL == "" {
		return InsertResult{}, fmt.Errorf("canonical url is required")
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return InsertResult{}, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return InsertResult{}, fmt.Errorf("begin insert tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Row lock against concurrent rewriter / editor saves on the same source.
	// Matches the pattern at backend/internal/entity/repository.go:421.
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM hatamoto_entities WHERE id = $1 FOR UPDATE`,
		in.SourceEntityID,
	); err != nil {
		return InsertResult{}, fmt.Errorf("lock source row: %w", err)
	}

	var content *string
	err = tx.QueryRow(ctx,
		`SELECT attributes->>'content' FROM hatamoto_entities WHERE id = $1`,
		in.SourceEntityID,
	).Scan(&content)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InsertResult{}, ErrSourceNotFound
		}
		return InsertResult{}, fmt.Errorf("load source content: %w", err)
	}

	currentContent := ""
	if content != nil {
		currentContent = *content
	}

	res, err := WrapAll(currentContent, []LinkSpec{{
		TargetEntityID:   in.TargetEntityID,
		TargetSlug:       in.TargetSlug,
		TargetSchemaSlug: in.TargetSchemaSlug,
		AnchorText:       in.AnchorText,
		CanonicalURL:     in.CanonicalURL,
	}})
	if err != nil {
		return InsertResult{}, fmt.Errorf("wrap: %w", err)
	}

	if res.Inserted == 0 {
		// Skip taxonomy collapses to the single key WrapAll populated.
		skip := SkipNoMatch
		for k := range res.Skips {
			skip = k
			break
		}
		return InsertResult{Inserted: false, SkipReason: skip}, nil
	}

	if _, err := tx.Exec(ctx,
		`UPDATE hatamoto_entities
		    SET attributes = jsonb_set(COALESCE(attributes, '{}'::jsonb), '{content}', to_jsonb($1::text), true),
		        updated_at = NOW()
		  WHERE id = $2`,
		res.NewHTML, in.SourceEntityID,
	); err != nil {
		return InsertResult{}, fmt.Errorf("update content: %w", err)
	}

	if err := reindexTx(ctx, tx, in.SourceEntityID); err != nil {
		return InsertResult{}, fmt.Errorf("reindex after insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return InsertResult{}, fmt.Errorf("commit insert: %w", err)
	}

	return InsertResult{
		Inserted:    true,
		NewContent:  res.NewHTML,
		ContentSize: len(res.NewHTML),
	}, nil
}
