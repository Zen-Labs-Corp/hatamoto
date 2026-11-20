//go:build integration

package backup

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/comment"
)

const (
	commentsSecret = "abc123" // matches the secrets.env written by seedAndDestroyWith
	commenterEmail = "reader@example.test"
	commentsDump   = `SELECT
		(SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.seq), '[]') FROM hatamoto_comments c)::text,
		(SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.email_hash), '[]') FROM hatamoto_commenters m)::text,
		(SELECT coalesce(jsonb_agg(to_jsonb(e) ORDER BY e.entity_id), '[]') FROM hatamoto_comment_entities e)::text,
		(SELECT to_jsonb(s)::text FROM hatamoto_comment_settings s)`
)

type commentsSnapshot struct{ comments, commenters, entities, settings string }

func readCommentsSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) commentsSnapshot {
	t.Helper()
	var s commentsSnapshot
	if err := pool.QueryRow(ctx, commentsDump).Scan(&s.comments, &s.commenters, &s.entities, &s.settings); err != nil {
		t.Fatalf("reading comment tables: %v", err)
	}
	return s
}

// TestRestoreIntegrationComments: comments, commenters (with the sealed email),
// per-entity counts and settings survive create, destroy and restore.
func TestRestoreIntegrationComments(t *testing.T) {
	ctx := context.Background()
	var before commentsSnapshot
	var maxSeq int64
	d := seedAndDestroyWith(t, func(ctx context.Context, pool *pgxpool.Pool) {
		k := comment.NewKeys(commentsSecret)
		hash := k.HashEmail(commenterEmail)
		sealed, err := k.SealEmail(commenterEmail, hash)
		if err != nil {
			t.Fatalf("SealEmail: %v", err)
		}
		var commenterID, entityID string
		if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_commenters (email_hash, email_sealed, trust_level, approved_count)
			VALUES ($1, $2, 'known', 2) RETURNING id::text`, hash, sealed).Scan(&commenterID); err != nil {
			t.Fatalf("seeding commenter: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT id::text FROM hatamoto_entities WHERE slug = 'restored-person'`).Scan(&entityID); err != nil {
			t.Fatal(err)
		}
		var rootSeq, replySeq int64
		if err := pool.QueryRow(ctx, `SELECT nextval('hatamoto_comments_seq')`).Scan(&rootSeq); err != nil {
			t.Fatal(err)
		}
		var rootID string
		rootPath := comment.Segment(rootSeq)
		if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_comments
			(seq, entity_id, path, depth, status, body_raw, body_html, commenter_id, author_name, ip_hash, net_hash)
			VALUES ($1, $2, $3, 1, 'approved', 'root body', '<p>root body</p>', $4, 'Reader', 'iphash', 'nethash')
			RETURNING id::text`, rootSeq, entityID, rootPath, commenterID).Scan(&rootID); err != nil {
			t.Fatalf("seeding root comment: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT nextval('hatamoto_comments_seq')`).Scan(&replySeq); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_comments
			(seq, entity_id, parent_id, path, depth, status, body_raw, body_html, commenter_id, author_name)
			VALUES ($1, $2, $3, $4, 2, 'pending', 'reply body', '<p>reply body</p>', $5, 'Reader')`,
			replySeq, entityID, rootID, rootPath+"."+comment.Segment(replySeq), commenterID); err != nil {
			t.Fatalf("seeding reply: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_comment_entities (entity_id, mode, comment_count, pending_count)
			VALUES ($1, 'closed', 1, 1)`, entityID); err != nil {
			t.Fatalf("seeding comment entity: %v", err)
		}
		maxSeq = replySeq
		before = readCommentsSnapshot(t, ctx, pool)
	})
	if before.comments == "" || maxSeq == 0 {
		t.Fatal("the seed hook did not run")
	}

	target := d.freshTarget(t, "hm_bk_comments")
	o, _, _, _ := d.options(t, target)
	if _, err := Restore(ctx, o, RestoreRequest{Source: d.archive, AdminURL: d.adminURL, ConfigOut: o.Data + ".env"}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	pool, err := pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// ISC-10: every comment-model row is identical, email_sealed bytes included.
	after := readCommentsSnapshot(t, ctx, pool)
	if after.comments != before.comments {
		t.Errorf("hatamoto_comments differ after restore:\nbefore %s\nafter  %s", before.comments, after.comments)
	}
	if after.commenters != before.commenters {
		t.Error("hatamoto_commenters differ after restore")
	}
	if after.entities != before.entities {
		t.Errorf("hatamoto_comment_entities differ after restore:\nbefore %s\nafter  %s", before.entities, after.entities)
	}
	if after.settings != before.settings {
		t.Errorf("hatamoto_comment_settings differ after restore:\nbefore %s\nafter  %s", before.settings, after.settings)
	}
	var enabled bool
	if err := pool.QueryRow(ctx, `SELECT enabled FROM hatamoto_comment_settings`).Scan(&enabled); err != nil || enabled {
		t.Errorf("comments enabled after restore = %v, %v", enabled, err)
	}

	// The sealed email opens with the secret from the restored secrets.env.
	var hash string
	var sealed []byte
	if err := pool.QueryRow(ctx, `SELECT email_hash, email_sealed FROM hatamoto_commenters`).Scan(&hash, &sealed); err != nil {
		t.Fatal(err)
	}
	if got, ok := comment.NewKeys(commentsSecret).OpenEmail(sealed, hash); !ok || got != commenterEmail {
		t.Error("the sealed commenter email does not open after restore")
	}

	// Thread shape intact.
	var parentOK bool
	if err := pool.QueryRow(ctx, `SELECT r.depth = 1 AND c.depth = 2 AND c.parent_id = r.id AND c.path = r.path || '.' || substr(c.path, length(r.path) + 2)
		FROM hatamoto_comments r JOIN hatamoto_comments c ON c.parent_id = r.id`).Scan(&parentOK); err != nil || !parentOK {
		t.Errorf("parent_id/path/depth not intact: %v, %v", parentOK, err)
	}

	// ISC-13: pg_dump restores the sequence state, so the next seq is past every
	// restored comment. (Advancing past imported seqs is comment.Resync, TODO-9.)
	var next int64
	if err := pool.QueryRow(ctx, `SELECT nextval('hatamoto_comments_seq')`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next <= maxSeq {
		t.Errorf("next seq after restore = %d, want > %d", next, maxSeq)
	}
}
