package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/net/html"

	"hatamoto.dev/core/internal/database"
)

// ParsedLink is a single occurrence of an <a data-entity-id="…"> tag inside
// rendered content. The OccurrenceIndex disambiguates multiple links from
// the same source to the same target — necessary because the index PK is
// (source, target, occurrence_index).
type ParsedLink struct {
	TargetEntityID  uuid.UUID
	AnchorText      string
	Href            string
	OccurrenceIndex int
}

// ParseLinksFromAttributes parses the full attributes JSON and returns every
// `<a data-entity-id>` tag from any wrap-target text leaf (top-level
// `content`/`full_description` plus every page_sections[*].content.*
// body/description/answer/etc.). The OccurrenceIndex is per-target across
// the entire entity so the index PK stays unique even when the same target
// is linked from multiple sections.
//
// Returns nil + nil if attrs is empty or has no linkable leaves.
func ParseLinksFromAttributes(attrs []byte) ([]ParsedLink, error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	var root map[string]any
	if err := json.Unmarshal(attrs, &root); err != nil {
		return nil, fmt.Errorf("unmarshal attributes: %w", err)
	}
	leaves := attributeLeaves(root)
	if len(leaves) == 0 {
		return nil, nil
	}

	var out []ParsedLink
	occurrences := map[uuid.UUID]int{}
	for _, leaf := range leaves {
		if leaf.value == "" {
			continue
		}
		root, err := html.Parse(strings.NewReader("<html><body>" + leaf.value + "</body></html>"))
		if err != nil {
			continue
		}
		walkAnchors(root, func(targetID uuid.UUID, href, anchor string) {
			idx := occurrences[targetID]
			occurrences[targetID] = idx + 1
			out = append(out, ParsedLink{
				TargetEntityID:  targetID,
				AnchorText:      anchor,
				Href:            href,
				OccurrenceIndex: idx,
			})
		})
	}
	return out, nil
}

// ParseLinks walks an HTML fragment and returns every <a> tag carrying a
// valid UUID data-entity-id, preserving document order. Tags without
// data-entity-id (hand-typed hrefs) are intentionally skipped — they are
// outside v2's index because we can't safely rewrite them on slug rename.
//
// HTML attribute names are normalised to lower-case by x/net/html, so the
// case of the source attribute (data-entity-id, DATA-ENTITY-ID, etc.)
// doesn't matter.
func ParseLinks(content string) ([]ParsedLink, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}

	// The same html.Parse + synthetic-wrapper pattern used in
	// the WP Gutenberg parser.
	// Wrapping in <html><body> keeps fragment-input round-tripping
	// predictable: x/net/html otherwise drops content that lives outside
	// the implicit <body>.
	root, err := html.Parse(strings.NewReader("<html><body>" + content + "</body></html>"))
	if err != nil {
		return nil, fmt.Errorf("parse content html: %w", err)
	}

	var links []ParsedLink
	occurrences := map[uuid.UUID]int{}
	walkAnchors(root, func(targetID uuid.UUID, href, anchor string) {
		idx := occurrences[targetID]
		occurrences[targetID] = idx + 1
		links = append(links, ParsedLink{
			TargetEntityID:  targetID,
			AnchorText:      anchor,
			Href:            href,
			OccurrenceIndex: idx,
		})
	})
	return links, nil
}

// walkAnchors descends the parsed tree, invoking visit for each anchor that
// has a usable data-entity-id. We extract anchor text by collapsing nested
// text nodes — formatting inside the <a> (an inner <em>, <strong>, …) is
// flattened to a single string for indexing purposes. The original DOM
// shape is preserved when the rewriter (see rewriter.go) only mutates href.
func walkAnchors(n *html.Node, visit func(targetID uuid.UUID, href, anchor string)) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
		var href, dataID string
		for _, a := range n.Attr {
			switch strings.ToLower(a.Key) {
			case "href":
				href = a.Val
			case "data-entity-id":
				dataID = a.Val
			}
		}
		if dataID != "" {
			if id, err := uuid.Parse(dataID); err == nil {
				visit(id, href, collectText(n))
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkAnchors(c, visit)
	}
}

func collectText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

// Reindex replaces the hatamoto_entity_link_index rows for a single source
// entity with whatever data-entity-id occurrences currently live in its
// stored attributes.content. Runs in a single transaction — readers always
// see either the old or new set, never a half-written intermediate.
//
// The hook in entity/service.go (added by Phase 1 §E) fires this after
// every UpdateEntity so the projection stays strongly consistent with
// content. The rewriter (rewriter.go) calls reindexTx inline to avoid
// nested transactions.
func Reindex(ctx context.Context, db *database.DB, sourceEntityID uuid.UUID) error {
	conn, err := db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reindex tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := reindexTx(ctx, tx, sourceEntityID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reindex: %w", err)
	}
	return nil
}

// reindexTx performs the delete + parse + insert sequence using an already-
// open transaction handle. Exposed package-internally so the rewriter can
// reuse the same logic without opening a nested transaction (pgx forbids
// concurrent operations on the same conn).
func reindexTx(ctx context.Context, tx pgx.Tx, sourceEntityID uuid.UUID) error {
	// Load the FULL attributes JSON so we can also walk section bodies for
	// <a data-entity-id> tags. Restricting to attributes->>'content' would
	// miss everything the JSONB backfill writes into
	// page_sections[*].content.body and friends.
	var attrs []byte
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(attributes, '{}'::jsonb) FROM hatamoto_entities WHERE id = $1`,
		sourceEntityID,
	).Scan(&attrs)
	if err != nil {
		return fmt.Errorf("load source attributes: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM hatamoto_entity_link_index WHERE source_entity_id = $1`,
		sourceEntityID,
	); err != nil {
		return fmt.Errorf("delete existing index rows: %w", err)
	}

	links, err := ParseLinksFromAttributes(attrs)
	if err != nil {
		return fmt.Errorf("parse attributes: %w", err)
	}
	if len(links) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, l := range links {
		batch.Queue(
			`INSERT INTO hatamoto_entity_link_index
				(source_entity_id, target_entity_id,
				 anchor_text, occurrence_index, href, updated_at)
			 VALUES ($1, $2, $3, $4, $5, NOW())`,
			sourceEntityID, l.TargetEntityID,
			l.AnchorText, l.OccurrenceIndex, l.Href,
		)
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for range links {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert index row: %w", err)
		}
	}
	return nil
}
