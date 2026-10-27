package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"hatamoto.dev/core/internal/database"
)

// Reason values for the audit log. Keep this list in sync with the
// migration comment on hatamoto_entity_link_audit.reason.
const (
	ReasonSlugChange   = "slug_change"
	ReasonBulkRetarget = "bulk_retarget"
	ReasonManual       = "manual"
)

// RewriteForEntity walks every source entity that links to targetEntityID
// (per hatamoto_entity_link_index) and rewrites the href on every matching
// <a data-entity-id="targetEntityID"> to newCanonicalURL. The anchor text
// and any other attributes are preserved.
//
// Concurrency model: per-source SELECT ... FOR UPDATE row lock for the
// duration of the rewrite transaction. ADR §8 documents that
// hatamoto_entities.version is auto-incremented but NOT enforced as an
// optimistic-concurrency gate (see entity/repository.go:451), so a row
// lock is the cheapest correct option without touching every other writer.
//
// One transaction per source: a failure to rewrite source A does not
// block rewriting source B. The caller (entity update hook) treats this
// best-effort — partial progress is logged, the next save reruns it.
//
// The reason argument is recorded in hatamoto_entity_link_audit and gates
// the one-click revert flow surfaced by Phase 5.
func RewriteForEntity(
	ctx context.Context,
	db *database.DB,
	targetEntityID uuid.UUID,
	newCanonicalURL string,
	reason string,
	actor *uuid.UUID,
) error {
	_, err := RewriteForEntityWithCount(ctx, db, targetEntityID, newCanonicalURL, reason, actor)
	return err
}

// RewriteForEntityWithCount is RewriteForEntity but also returns the number
// of source entities touched. Used by the equity dashboard's bulk-retarget
// action so the UI can surface "N entities updated".
func RewriteForEntityWithCount(
	ctx context.Context,
	db *database.DB,
	targetEntityID uuid.UUID,
	newCanonicalURL string,
	reason string,
	actor *uuid.UUID,
) (int, error) {
	if newCanonicalURL == "" {
		return 0, fmt.Errorf("rewrite: new canonical url is empty")
	}

	sourceIDs, err := loadSourceIDs(ctx, db, targetEntityID)
	if err != nil {
		return 0, err
	}
	if len(sourceIDs) == 0 {
		return 0, nil
	}

	var firstErr error
	for _, srcID := range sourceIDs {
		if err := rewriteOneSource(ctx, db, srcID, targetEntityID, newCanonicalURL, reason, actor); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return len(sourceIDs), firstErr
}

func loadSourceIDs(ctx context.Context, db *database.DB, targetEntityID uuid.UUID) ([]uuid.UUID, error) {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx,
		`SELECT DISTINCT source_entity_id
		 FROM hatamoto_entity_link_index
		 WHERE target_entity_id = $1`,
		targetEntityID,
	)
	if err != nil {
		return nil, fmt.Errorf("query source ids: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan source id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func rewriteOneSource(
	ctx context.Context,
	db *database.DB,
	sourceID, targetID uuid.UUID,
	newCanonicalURL, reason string,
	actor *uuid.UUID,
) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn for source %s: %w", sourceID, err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin rewrite tx for source %s: %w", sourceID, err)
	}
	defer tx.Rollback(ctx)

	// Take the same lock the entity Update path takes (repository.go:421)
	// so a concurrent admin save and the rewriter cannot clobber each other.
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM hatamoto_entities WHERE id = $1 FOR UPDATE`,
		sourceID,
	); err != nil {
		return fmt.Errorf("lock source %s: %w", sourceID, err)
	}

	// Load the FULL attributes JSONB — not just `->>'content'`. v2 links
	// can live in any wrap-target leaf (top-level content, full_description,
	// page_sections[*].content.body / description / paragraph / answer /
	// problemDescription / solutionDescription). Industry-vertical and
	// service entities keep their content in sections and have an empty
	// top-level `content`; reading just `->>'content'` would skip them.
	var attrs []byte
	if err := tx.QueryRow(ctx,
		`SELECT attributes FROM hatamoto_entities WHERE id = $1`,
		sourceID,
	).Scan(&attrs); err != nil {
		return fmt.Errorf("load attributes under lock for source %s: %w", sourceID, err)
	}
	if len(attrs) == 0 {
		return nil
	}

	newAttrs, rewrites, err := rewriteHrefsInAttributes(attrs, targetID, newCanonicalURL)
	if err != nil {
		return fmt.Errorf("rewrite attributes for source %s: %w", sourceID, err)
	}
	if len(rewrites) == 0 {
		// Source was in the index but no matching <a> survives — content
		// was hand-edited between indexing and rewrite. Reindex to drop
		// the stale rows so the next rewrite isn't a no-op.
		if err := reindexTx(ctx, tx, sourceID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE hatamoto_entities
		 SET attributes = $1::jsonb,
		     updated_at = NOW()
		 WHERE id = $2`,
		newAttrs, sourceID,
	); err != nil {
		return fmt.Errorf("update attributes for source %s: %w", sourceID, err)
	}

	if err := reindexTx(ctx, tx, sourceID); err != nil {
		return err
	}

	for _, rw := range rewrites {
		if _, err := tx.Exec(ctx,
			`INSERT INTO hatamoto_entity_link_audit
				(source_entity_id, target_entity_id, old_href, new_href, anchor_text, reason, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			sourceID, targetID, rw.oldHref, rw.newHref, rw.anchor, reason, actor,
		); err != nil {
			return fmt.Errorf("write audit for source %s: %w", sourceID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rewrite for source %s: %w", sourceID, err)
	}
	return nil
}

type rewriteRecord struct {
	oldHref string
	newHref string
	anchor  string
}

// RewriteSingleLink rewrites exactly one inbound link — the Nth occurrence
// of `<a data-entity-id=targetID>` inside sourceID's content — to use
// newCanonicalURL. Every other link in the source is left alone, including
// other occurrences of the same target.
//
// Used by the equity dashboard's per-link retarget action: an admin
// clicking "Retarget" on a single drill-down row changes ONLY that row.
//
// Concurrency: same FOR UPDATE lock pattern as rewriteOneSource so a
// concurrent editor save and the rewriter can't clobber each other.
// Audits the change with reason="manual" by default (caller may pass a
// different reason, e.g. ReasonBulkRetarget for a future bulk-per-link
// flow).
//
// Returns nil on a no-op (the Nth occurrence's href already matches
// newCanonicalURL or the target/occurrence pair no longer exists in the
// source content; in the latter case the index is also reindexed so the
// stale row is dropped).
func RewriteSingleLink(
	ctx context.Context,
	db *database.DB,
	sourceID, targetID uuid.UUID,
	occurrenceIndex int,
	newCanonicalURL, reason string,
	actor *uuid.UUID,
) error {
	if newCanonicalURL == "" {
		return fmt.Errorf("rewrite-single: new canonical url is empty")
	}
	if occurrenceIndex < 0 {
		return fmt.Errorf("rewrite-single: occurrence_index must be >= 0")
	}

	conn, err := db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM hatamoto_entities WHERE id = $1 FOR UPDATE`,
		sourceID,
	); err != nil {
		return fmt.Errorf("lock source %s: %w", sourceID, err)
	}

	// Load FULL attributes — see rewriteOneSource for why `->>'content'`
	// alone is wrong (industry-vertical and service entities keep links in
	// page_sections, not top-level content).
	var attrs []byte
	if err := tx.QueryRow(ctx,
		`SELECT attributes FROM hatamoto_entities WHERE id = $1`,
		sourceID,
	).Scan(&attrs); err != nil {
		return fmt.Errorf("load source attributes: %w", err)
	}
	if len(attrs) == 0 {
		if err := reindexTx(ctx, tx, sourceID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	newAttrs, rec, err := rewriteSingleHrefInAttributes(attrs, targetID, occurrenceIndex, newCanonicalURL)
	if err != nil {
		return fmt.Errorf("rewrite attributes: %w", err)
	}
	if rec == nil {
		// Nth occurrence didn't exist anywhere in current content — the editor
		// likely removed it after the index row was written. Reindex so future
		// dashboard reads don't show the ghost.
		if err := reindexTx(ctx, tx, sourceID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if rec.oldHref == rec.newHref {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE hatamoto_entities
		 SET attributes = $1::jsonb,
		     updated_at = NOW()
		 WHERE id = $2`,
		newAttrs, sourceID,
	); err != nil {
		return fmt.Errorf("update attributes: %w", err)
	}
	if err := reindexTx(ctx, tx, sourceID); err != nil {
		return err
	}

	if reason == "" {
		reason = ReasonManual
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO hatamoto_entity_link_audit
			(source_entity_id, target_entity_id, old_href, new_href, anchor_text, reason, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		sourceID, targetID, rec.oldHref, rec.newHref, rec.anchor, reason, actor,
	); err != nil {
		return fmt.Errorf("write audit: %w", err)
	}

	return tx.Commit(ctx)
}

// rewriteSingleHref walks contentHTML and rewrites the Nth
// `<a data-entity-id=targetID>` tag's href to newCanonicalURL. Anchor
// text and other attributes are untouched.
//
// occurrenceIndex is zero-based and counts ONLY tags matching targetID;
// other `<a data-entity-id>` tags pointing at different targets do not
// advance the counter.
//
// Returns the rewritten HTML and a one-element record on success, or
// (contentHTML, nil, nil) when the Nth occurrence does not exist.
func rewriteSingleHref(contentHTML string, targetID uuid.UUID, occurrenceIndex int, newCanonicalURL string) (string, *rewriteRecord, error) {
	root, err := html.Parse(strings.NewReader("<html><body>" + contentHTML + "</body></html>"))
	if err != nil {
		return "", nil, fmt.Errorf("parse content: %w", err)
	}
	body := findBody(root)
	if body == nil {
		return contentHTML, nil, nil
	}

	var rec *rewriteRecord
	seen := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if rec != nil {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			var hrefIdx = -1
			var dataID string
			for i, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "href":
					hrefIdx = i
				case "data-entity-id":
					dataID = a.Val
				}
			}
			if dataID != "" {
				if id, err := uuid.Parse(dataID); err == nil && id == targetID && hrefIdx >= 0 {
					if seen == occurrenceIndex {
						old := n.Attr[hrefIdx].Val
						n.Attr[hrefIdx].Val = newCanonicalURL
						rec = &rewriteRecord{
							oldHref: old,
							newHref: newCanonicalURL,
							anchor:  collectText(n),
						}
						return
					}
					seen++
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if rec != nil {
				return
			}
		}
	}
	walk(body)

	if rec == nil {
		return contentHTML, nil, nil
	}

	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&sb, c); err != nil {
			return "", nil, fmt.Errorf("render content: %w", err)
		}
	}
	return sb.String(), rec, nil
}

// rewriteHrefsForEntity DOM-walks contentHTML and replaces the href of every
// <a data-entity-id="targetID"> with newCanonicalURL. Anchor text and other
// attributes are untouched. Returns the new HTML and the per-anchor diff.
//
// The x/net/html parser wraps fragments in <html><body>…; we strip the
// synthetic wrapper before returning so stored content stays a fragment.
// This mirrors the seedutil pattern used by the WP importer.
func rewriteHrefsForEntity(contentHTML string, targetID uuid.UUID, newCanonicalURL string) (string, []rewriteRecord, error) {
	root, err := html.Parse(strings.NewReader("<html><body>" + contentHTML + "</body></html>"))
	if err != nil {
		return "", nil, fmt.Errorf("parse content: %w", err)
	}
	body := findBody(root)
	if body == nil {
		return contentHTML, nil, nil
	}

	var rewrites []rewriteRecord
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			var hrefIdx = -1
			var dataID string
			for i, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "href":
					hrefIdx = i
				case "data-entity-id":
					dataID = a.Val
				}
			}
			if dataID != "" {
				if id, err := uuid.Parse(dataID); err == nil && id == targetID && hrefIdx >= 0 {
					old := n.Attr[hrefIdx].Val
					if old != newCanonicalURL {
						n.Attr[hrefIdx].Val = newCanonicalURL
						rewrites = append(rewrites, rewriteRecord{
							oldHref: old,
							newHref: newCanonicalURL,
							anchor:  collectText(n),
						})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(body)

	if len(rewrites) == 0 {
		return contentHTML, nil, nil
	}

	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&sb, c); err != nil {
			return "", nil, fmt.Errorf("render content: %w", err)
		}
	}
	return sb.String(), rewrites, nil
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "body" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

// rewriteHrefsInAttributes walks every wrap-target text leaf in the full
// attributes JSONB (top-level content, full_description, plus every
// page_sections[*].content.* body field tracked by wrap_jsonb.go) and
// rewrites EVERY `<a data-entity-id=targetID>` href to newCanonicalURL.
//
// Returns the mutated JSON bytes and a record per rewritten anchor. If no
// matching anchor exists, returns the input bytes unchanged with an empty
// record slice.
//
// Mirrors the leaf-walk pattern from ParseLinksFromAttributes (indexer.go)
// so the indexer and rewriter agree on what content counts.
func rewriteHrefsInAttributes(attrs []byte, targetID uuid.UUID, newCanonicalURL string) ([]byte, []rewriteRecord, error) {
	if len(attrs) == 0 {
		return attrs, nil, nil
	}
	var root map[string]any
	if err := json.Unmarshal(attrs, &root); err != nil {
		return attrs, nil, fmt.Errorf("unmarshal attributes: %w", err)
	}

	leaves := attributeLeaves(root)
	if len(leaves) == 0 {
		return attrs, nil, nil
	}

	var all []rewriteRecord
	changed := false
	for i := range leaves {
		leaf := &leaves[i]
		if leaf.value == "" || !strings.Contains(leaf.value, "data-entity-id") {
			continue
		}
		newLeaf, recs, err := rewriteHrefsForEntity(leaf.value, targetID, newCanonicalURL)
		if err != nil {
			return attrs, nil, err
		}
		if len(recs) == 0 {
			continue
		}
		leaf.value = newLeaf
		leaf.set(newLeaf)
		all = append(all, recs...)
		changed = true
	}

	if !changed {
		return attrs, nil, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return attrs, nil, fmt.Errorf("re-marshal attributes: %w", err)
	}
	return out, all, nil
}

// rewriteSingleHrefInAttributes walks the full attributes JSONB looking
// for the Nth `<a data-entity-id=targetID>` anchor (counted in
// document order across leaves, matching ParseLinksFromAttributes's
// occurrence ordering) and rewrites just that one's href.
//
// Returns the mutated JSON bytes and a single-anchor record on success.
// Returns (attrs, nil, nil) when the Nth occurrence does not exist —
// caller treats that as a stale-index signal and reindexes.
func rewriteSingleHrefInAttributes(attrs []byte, targetID uuid.UUID, occurrenceIndex int, newCanonicalURL string) ([]byte, *rewriteRecord, error) {
	if len(attrs) == 0 {
		return attrs, nil, nil
	}
	var root map[string]any
	if err := json.Unmarshal(attrs, &root); err != nil {
		return attrs, nil, fmt.Errorf("unmarshal attributes: %w", err)
	}

	leaves := attributeLeaves(root)
	if len(leaves) == 0 {
		return attrs, nil, nil
	}

	// Count of matching tags seen across leaves so far. occurrenceIndex is
	// zero-based and per-target, matching the indexer.
	seen := 0
	var hit *rewriteRecord
	for i := range leaves {
		leaf := &leaves[i]
		if leaf.value == "" || !strings.Contains(leaf.value, "data-entity-id") {
			continue
		}
		// How many target anchors does this leaf contain? Walk first to find
		// out, then decide whether the requested occurrence lives in this
		// leaf. If yes, do the rewrite at the local index = occurrenceIndex - seen.
		count := countTargetAnchorsInLeaf(leaf.value, targetID)
		if count == 0 {
			continue
		}
		if seen+count <= occurrenceIndex {
			seen += count
			continue
		}
		localIdx := occurrenceIndex - seen
		newLeaf, rec, err := rewriteSingleHref(leaf.value, targetID, localIdx, newCanonicalURL)
		if err != nil {
			return attrs, nil, err
		}
		if rec == nil {
			// Index disagrees with current content — give up; caller reindexes.
			return attrs, nil, nil
		}
		leaf.value = newLeaf
		leaf.set(newLeaf)
		hit = rec
		break
	}

	if hit == nil {
		return attrs, nil, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return attrs, nil, fmt.Errorf("re-marshal attributes: %w", err)
	}
	return out, hit, nil
}

// countTargetAnchorsInLeaf parses one HTML string and returns the count of
// `<a data-entity-id=targetID>` tags. Cheap pre-walk so the per-leaf
// rewriter knows which leaf contains the requested occurrence without
// having to rewrite-and-roll-back.
func countTargetAnchorsInLeaf(leafHTML string, targetID uuid.UUID) int {
	leafHTML = strings.TrimSpace(leafHTML)
	if leafHTML == "" {
		return 0
	}
	root, err := html.Parse(strings.NewReader("<html><body>" + leafHTML + "</body></html>"))
	if err != nil {
		return 0
	}
	body := findBody(root)
	if body == nil {
		return 0
	}
	count := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			for _, a := range n.Attr {
				if strings.EqualFold(a.Key, "data-entity-id") {
					if id, err := uuid.Parse(a.Val); err == nil && id == targetID {
						count++
					}
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(body)
	return count
}
