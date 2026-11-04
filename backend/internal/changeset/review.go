package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// The limits of one changeset, exported for the review page's limits card.
const (
	MaxStagedItems = 50000 // keep equal to maxStagedItems
	MaxStagedBytes = int64(256 << 20)
)

// ErrNotClassified means an item has no risk yet: call Engine.Classify first.
var ErrNotClassified = errors.New("changeset items are not classified")

// readBatch is how many items the reads that walk a whole changeset take at a time.
const readBatch = 1000

// KindCount is how many items of one kind a changeset creates, updates and removes.
type KindCount struct {
	Kind                  Kind
	New, Updated, Removed int
}

// ReviewCounts is the summary a reviewer sees: items by risk, how many risky
// ones are decided, how many are dropped, and what the changeset holds.
type ReviewCounts struct {
	Low, High, Critical int
	// HighDecided counts the high and critical items that have a decision.
	HighDecided  int
	Dropped      int
	ByKind       []KindCount
	PayloadBytes int64
}

// readTx opens a read-only transaction on the pool for the reads that reuse
// the engine's transaction helpers. The caller rolls it back.
func (e *Engine) readTx(ctx context.Context) (pgx.Tx, error) {
	return e.DB.Pool().BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
}

// ReviewCounts reads the summary of a changeset. It never writes. ErrNotFound
// for an unknown changeset, ErrNotClassified while an item has no risk.
func (e *Engine) ReviewCounts(ctx context.Context, csID int64) (ReviewCounts, error) {
	var c ReviewCounts
	var unclassified int
	err := e.DB.Pool().QueryRow(ctx, `
		SELECT c.payload_bytes,
		       count(*) FILTER (WHERE i.risk IS NULL),
		       count(*) FILTER (WHERE i.risk = 'low'),
		       count(*) FILTER (WHERE i.risk = 'high'),
		       count(*) FILTER (WHERE i.risk = 'critical'),
		       count(*) FILTER (WHERE i.risk IN ('high', 'critical') AND i.decision IS NOT NULL),
		       count(*) FILTER (WHERE i.decision = 'dropped' OR i.result = 'dropped'
		            OR (i.risk_reason = 'redirect' AND COALESCE(i.risk_ref, 0) > 0 AND EXISTS (
		                SELECT 1 FROM hatamoto_changeset_items p
		                WHERE p.changeset_id = i.changeset_id AND p.seq = i.risk_ref AND p.decision = 'dropped')))
		FROM hatamoto_changesets c LEFT JOIN hatamoto_changeset_items i ON i.changeset_id = c.id
		WHERE c.id = $1 GROUP BY c.id`, csID).
		Scan(&c.PayloadBytes, &unclassified, &c.Low, &c.High, &c.Critical, &c.HighDecided, &c.Dropped)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, fmt.Errorf("failed to count items: %w", err)
	}
	if unclassified > 0 {
		return c, ErrNotClassified
	}
	// New is an upsert that creates: nothing to update yet in a staged
	// changeset, an applied item without a pre-image in a published one.
	rows, err := e.DB.Pool().Query(ctx, `
		SELECT i.kind,
		       count(*) FILTER (WHERE i.op = 'upsert' AND CASE WHEN c.state = 'staged'
		            THEN COALESCE(i.target_id, '') = '' AND sk.target_id IS NULL
		            ELSE i.result = 'applied' AND i.before IS NULL END),
		       count(*) FILTER (WHERE i.op = 'upsert' AND NOT (CASE WHEN c.state = 'staged'
		            THEN COALESCE(i.target_id, '') = '' AND sk.target_id IS NULL
		            ELSE i.result = 'applied' AND i.before IS NULL END)),
		       count(*) FILTER (WHERE i.op = 'delete')
		FROM hatamoto_changesets c
		JOIN hatamoto_changeset_items i ON i.changeset_id = c.id
		LEFT JOIN hatamoto_changeset_source_keys sk
		       ON sk.origin = CASE WHEN c.plugin = '' THEN 'owner' ELSE 'plugin:' || c.plugin END
		      AND sk.kind = i.kind AND i.source_key <> '' AND sk.source_key = i.source_key
		WHERE c.id = $1 GROUP BY i.kind`, csID)
	if err != nil {
		return c, fmt.Errorf("failed to count kinds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k KindCount
		var kind string
		if err := rows.Scan(&kind, &k.New, &k.Updated, &k.Removed); err != nil {
			return c, err
		}
		k.Kind = Kind(kind)
		c.ByKind = append(c.ByKind, k)
	}
	if err := rows.Err(); err != nil {
		return c, err
	}
	order := []Kind{KindEntity, KindTerm, KindMenu, KindRedirect, KindMedia}
	slices.SortFunc(c.ByKind, func(a, b KindCount) int { return slices.Index(order, a.Kind) - slices.Index(order, b.Kind) })
	return c, nil
}

// ReviewItem is one item of the review list: the summary, its risk, the
// decision on it, and short strings for the before and after of the change.
type ReviewItem struct {
	ItemSummary
	Risk     string
	Reason   string
	Ref      int
	Decision string
	// Old and New are what a link, slug or redirect row shows: the live value
	// and the staged one. Either may be empty.
	Old, New string
}

// ReviewItems reads limit items from offset, ordered by seq, limited to the
// risks given (none: all). Payloads are read for the page only. ErrNotClassified
// while an item of the page has no risk.
func (e *Engine) ReviewItems(ctx context.Context, csID int64, risks []string, limit, offset int) ([]ReviewItem, error) {
	return e.reviewItems(ctx, csID, risks, false, limit, offset)
}

// ReviewRiskyItems reads limit of the high and critical items from offset, ordered
// by seq, leaving out the redirects that are decided with their page (the
// redirect group). It is the page of the itemised risky list.
func (e *Engine) ReviewRiskyItems(ctx context.Context, csID int64, limit, offset int) ([]ReviewItem, error) {
	return e.reviewItems(ctx, csID, []string{string(RiskHigh), string(RiskCritical)}, true, limit, offset)
}

func (e *Engine) reviewItems(ctx context.Context, csID int64, risks []string, itemised bool, limit, offset int) ([]ReviewItem, error) {
	var plugin string
	if err := e.DB.Pool().QueryRow(ctx, `SELECT plugin FROM hatamoto_changesets WHERE id = $1`, csID).Scan(&plugin); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed to read changeset: %w", err)
	}
	if risks == nil {
		risks = []string{}
	}
	rows, err := e.DB.Pool().Query(ctx, `
		SELECT seq, kind, op, source_key, COALESCE(target_id, ''),
		       COALESCE(NULLIF(payload->>'name', ''), NULLIF(payload->>'title', ''), NULLIF(payload->>'source_path', ''), NULLIF(payload->>'slug', ''), ''),
		       risk, COALESCE(risk_reason, ''), COALESCE(risk_ref, 0), COALESCE(decision, ''), payload
		FROM hatamoto_changeset_items
		WHERE changeset_id = $1 AND (cardinality($2::text[]) = 0 OR risk = ANY($2))
		  AND (NOT $5 OR NOT (risk_reason = 'redirect' AND COALESCE(risk_ref, 0) > 0))
		ORDER BY seq LIMIT $3 OFFSET $4`, csID, risks, limit, offset, itemised)
	if err != nil {
		return nil, fmt.Errorf("failed to read items: %w", err)
	}
	type row struct {
		ReviewItem
		payload json.RawMessage
	}
	var page []row
	for rows.Next() {
		var r row
		var kind string
		var risk *string
		var payload []byte
		if err := rows.Scan(&r.Seq, &kind, &r.Op, &r.SourceKey, &r.TargetID, &r.Label, &risk, &r.Reason, &r.Ref, &r.Decision, &payload); err != nil {
			rows.Close()
			return nil, err
		}
		if risk == nil {
			rows.Close()
			return nil, ErrNotClassified
		}
		r.Kind, r.Risk, r.payload = Kind(kind), *risk, json.RawMessage(payload)
		page = append(page, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tx, err := e.readTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin read: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only
	out := make([]ReviewItem, 0, len(page))
	for _, r := range page {
		it := StoredItem{Item: Item{Kind: r.Kind, Op: r.Op, SourceKey: r.SourceKey, TargetID: r.TargetID, Payload: r.payload}}
		live, err := loadLive(ctx, tx, Origin(plugin), it)
		if err != nil {
			return nil, fmt.Errorf("item %d (%s): %w", r.Seq, r.Kind, err)
		}
		r.Old, r.New = displayChange(it.Item, RiskReason(r.Reason), live, r.Label)
		out = append(out, r.ReviewItem)
	}
	return out, nil
}

// displayChange picks the before and after strings a risky row shows.
func displayChange(it Item, reason RiskReason, live *Live, label string) (oldS, newS string) {
	var p struct {
		Slug       string          `json:"slug"`
		OldAlt     string          `json:"old_alt"`
		AltText    string          `json:"alt_text"`
		SourcePath string          `json:"source_path"`
		Target     string          `json:"target"`
		Attributes json.RawMessage `json:"attributes"`
		Items      []struct {
			URL *string `json:"url"`
		} `json:"items"`
	}
	_ = json.Unmarshal(it.Payload, &p)
	switch reason {
	case ReasonSlug:
		if live != nil {
			oldS = live.Slug
		}
		return oldS, p.Slug
	case ReasonRedirect:
		return p.SourcePath, p.Target
	case ReasonDelete:
		return label, ""
	case ReasonAltText:
		return p.OldAlt, p.AltText
	case ReasonMenuLink:
		for _, mi := range p.Items {
			if mi.URL != nil {
				if _, host, _, _, ok := splitLink(*mi.URL); ok && host != "" {
					return "", *mi.URL
				}
			}
		}
	case ReasonLinkHost, ReasonLinkQuery:
		var attrs any
		if json.Unmarshal(p.Attributes, &attrs) != nil {
			return "", ""
		}
		var liveLinks linkSet
		if live != nil {
			var la any
			if json.Unmarshal(live.Attributes, &la) == nil {
				liveLinks = scanStrings(la).links
			}
		}
		for _, l := range scanStrings(attrs).links {
			if reason == ReasonLinkHost && l.host != "" && !liveLinks.hasHost(l.host) {
				return "", l.display(l.query)
			}
			if known := liveLinks.queries(l); reason == ReasonLinkQuery && known != nil && !known[l.query] {
				for q := range known {
					return l.display(q), l.display(l.query)
				}
			}
		}
	}
	return "", ""
}

// display is the link as a short string: host, path and the given query.
func (l link) display(query string) string {
	s := l.host + l.path
	if query != "" {
		s += "?" + query
	}
	return s
}

// StaleSeqs lists the items of a staged changeset whose live object changed
// since the plugin read it (its base token no longer matches): publish would
// skip them. ErrNotStaged for any other state. It never writes.
func (e *Engine) StaleSeqs(ctx context.Context, csID int64) ([]ItemSummary, error) {
	var plugin, state string
	if err := e.DB.Pool().QueryRow(ctx, `SELECT plugin, state FROM hatamoto_changesets WHERE id = $1`, csID).Scan(&plugin, &state); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed to read changeset: %w", err)
	}
	if state != StateStaged {
		return nil, ErrNotStaged
	}
	tx, err := e.readTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin read: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only
	var out []ItemSummary
	for last := 0; ; {
		batch, err := readBatchOf(ctx, tx, csID, last, `AND base_token IS NOT NULL`)
		if err != nil {
			return nil, err
		}
		for _, b := range batch {
			last = b.Seq
			target := b.TargetID
			if target == "" {
				if target, err = mappedTarget(ctx, tx, Origin(plugin), b.Kind, b.SourceKey); err != nil {
					return nil, err
				}
			}
			if target == "" {
				continue
			}
			tok, err := liveToken(ctx, tx, b.Kind, target)
			if errors.Is(err, ErrInvalidItem) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if tok != b.BaseToken {
				out = append(out, b.ItemSummary)
			}
		}
		if len(batch) < readBatch {
			return out, nil
		}
	}
}

type batchItem struct {
	ItemSummary
	BaseToken, AfterToken string
}

// readBatchOf reads up to readBatch items after seq last, with the extra
// condition (a constant SQL fragment).
func readBatchOf(ctx context.Context, tx pgx.Tx, csID int64, last int, cond string) ([]batchItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT seq, kind, op, source_key, COALESCE(target_id, ''),
		       COALESCE(NULLIF(payload->>'name', ''), NULLIF(payload->>'title', ''), NULLIF(payload->>'source_path', ''), NULLIF(payload->>'slug', ''), ''),
		       COALESCE(base_token, ''), COALESCE(after_token, '')
		FROM hatamoto_changeset_items WHERE changeset_id = $1 AND seq > $2 `+cond+` ORDER BY seq LIMIT $3`, csID, last, readBatch)
	if err != nil {
		return nil, fmt.Errorf("failed to read items: %w", err)
	}
	defer rows.Close()
	var out []batchItem
	for rows.Next() {
		var b batchItem
		var kind string
		if err := rows.Scan(&b.Seq, &kind, &b.Op, &b.SourceKey, &b.TargetID, &b.Label, &b.BaseToken, &b.AfterToken); err != nil {
			return nil, err
		}
		b.Kind = Kind(kind)
		out = append(out, b)
	}
	return out, rows.Err()
}

// HistoryRow is one changeset in the history list.
type HistoryRow struct {
	Header
	RunName, RunState         string
	PublishedAt, RevertedAt   *time.Time
	Applied, Skipped, Dropped int
	PayloadBytes              int64
	Auto                      bool // published under a standing approval
}

// List reads changesets newest first, limited to plugin ("" is all plugins).
// Applied counts the items a publish applied, reverted ones included.
func (e *Engine) List(ctx context.Context, plugin string, limit, offset int) ([]HistoryRow, error) {
	rows, err := e.DB.Pool().Query(ctx, `
		SELECT c.id, c.run_id, c.plugin, c.state, c.expires_at, c.item_count, r.name, r.state, c.published_at, c.reverted_at, c.payload_bytes, c.auto_published,
		       count(i.id) FILTER (WHERE i.result IN ('applied', 'reverted')),
		       count(i.id) FILTER (WHERE i.result IN ('skipped_conflict', 'skipped_other')),
		       count(i.id) FILTER (WHERE i.result = 'dropped')
		FROM hatamoto_changesets c
		JOIN hatamoto_runs r ON r.id = c.run_id
		LEFT JOIN hatamoto_changeset_items i ON i.changeset_id = c.id
		WHERE $1 = '' OR c.plugin = $1
		GROUP BY c.id, r.name, r.state
		ORDER BY c.created_at DESC, c.id DESC LIMIT $2 OFFSET $3`, plugin, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list changesets: %w", err)
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		var h HistoryRow
		if err := rows.Scan(&h.ID, &h.RunID, &h.Plugin, &h.State, &h.ExpiresAt, &h.ItemCount, &h.RunName, &h.RunState, &h.PublishedAt, &h.RevertedAt,
			&h.PayloadBytes, &h.Auto, &h.Applied, &h.Skipped, &h.Dropped); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// RevertPreview is the dry run of Revert: how many applied items of a
// published changeset a revert would leave alone because their live object
// changed since the publish (movedSince), out of how many it would restore
// (total). It never writes. ErrNotPublished for any other state.
func (e *Engine) RevertPreview(ctx context.Context, csID int64) (movedSince int, total int, err error) {
	var state string
	if err := e.DB.Pool().QueryRow(ctx, `SELECT state FROM hatamoto_changesets WHERE id = $1`, csID).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrNotFound
	} else if err != nil {
		return 0, 0, fmt.Errorf("failed to read changeset: %w", err)
	}
	if state != StatePublished {
		return 0, 0, ErrNotPublished
	}
	tx, err := e.readTx(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin read: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only
	for last := 0; ; {
		batch, err := readBatchOf(ctx, tx, csID, last, `AND result = 'applied'`)
		if err != nil {
			return 0, 0, err
		}
		for _, b := range batch {
			last = b.Seq
			total++
			moved, err := e.movedSincePublish(ctx, tx, StoredItem{Item: Item{Kind: b.Kind, TargetID: b.TargetID}, AfterToken: b.AfterToken}, nil)
			if err != nil {
				return 0, 0, fmt.Errorf("item %d (%s): %w", b.Seq, b.Kind, err)
			}
			if moved {
				movedSince++
			}
		}
		if len(batch) < readBatch {
			return movedSince, total, nil
		}
	}
}
