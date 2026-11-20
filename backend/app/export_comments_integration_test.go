//go:build integration

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/portability"
)

const commentsTestSecret = "comments-round-trip-secret"

var commentTables = []string{"hatamoto_commenters", "hatamoto_comments"}

// stripComments rewrites an archive into the shape a binary from before
// comments wrote: no comment files, and no mention of them in the header or
// manifest.
func stripComments(t *testing.T, raw []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	tr := tar.NewReader(gz)
	isComment := func(n string) bool {
		for _, c := range commentTables {
			if n == c {
				return true
			}
		}
		return false
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		switch h.Name {
		case "data/hatamoto_commenters.jsonl", "data/hatamoto_comments.jsonl":
			continue
		case "format.json":
			var hdr map[string]any
			_ = jsonx.Unmarshal(b, &hdr)
			var kept []any
			for _, n := range hdr["tables"].([]any) {
				if !isComment(n.(string)) {
					kept = append(kept, n)
				}
			}
			hdr["tables"] = kept
			b, _ = jsonx.Marshal(hdr)
		case "manifest.json":
			var m map[string]any
			_ = jsonx.Unmarshal(b, &m)
			for _, c := range commentTables {
				delete(m["rows"].(map[string]any), c)
			}
			b, _ = jsonx.Marshal(m)
		}
		h.Size = int64(len(b))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Comments and commenters survive export and import byte for byte, the counts
// are rebuilt, the sequence moves past the imported rows, and the older and
// --no-submissions archive shapes import with no comments.
func TestCommentsRoundTrip_Integration(t *testing.T) {
	ctx := context.Background()
	srcDSN, src := scratchDB(t)
	dstDSN, dst := scratchDB(t)
	_, bare := scratchDB(t)
	_, older := scratchDB(t)
	for _, p := range []*pgxpool.Pool{src, dst, bare, older} {
		if err := migrateUp(ctx, p, quietLogger()); err != nil {
			t.Fatal(err)
		}
	}
	commentDB := func(dsn string) *database.DB {
		t.Helper()
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		db, err := database.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(db.Close)
		return db
	}
	srcDB, dstDB := commentDB(srcDSN), commentDB(dstDSN)
	svc := comment.New(srcDB, commentsTestSecret, quietLogger())

	// ---- seed with the shipped service so seq, path and counts are real
	var post uuid.UUID
	if err := src.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id,name,slug,status,published_at)
		SELECT id,'Hello Post','hello-post','published',now() FROM hatamoto_entity_schemas WHERE slug='post' RETURNING id`).Scan(&post); err != nil {
		t.Fatal(err)
	}
	store := func(s *comment.Service, entity uuid.UUID, parent *uuid.UUID, email, raw string, status comment.Status) comment.Comment {
		t.Helper()
		r, err := commentfmt.Render(raw)
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.Store(ctx, comment.Draft{EntityID: entity, ParentID: parent, Email: email, AuthorName: "Reader",
			BodyRaw: raw, BodyHTML: r.HTML, HasLinks: r.HasLinks, LinkDomains: r.LinkDomains, BodyHash: r.BodyHash, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	root := store(svc, post, nil, "reader@example.test", "First comment", comment.StatusApproved)
	store(svc, post, &root.ID, "reader@example.test", "A reply", comment.StatusApproved)
	store(svc, post, nil, "other@example.test", "Waiting for review", comment.StatusPending)
	store(svc, post, nil, "spam@example.test", "Cheap pills", comment.StatusSpam)
	gone := store(svc, post, nil, "reader@example.test", "Removed one", comment.StatusApproved)
	if _, err := svc.Trash(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	scalar := func(p *pgxpool.Pool, q string, args ...any) string {
		t.Helper()
		var s string
		if err := p.QueryRow(ctx, q, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return s
	}
	var srcMax int64
	if err := src.QueryRow(ctx, `SELECT max(seq) FROM hatamoto_comments`).Scan(&srcMax); err != nil {
		t.Fatal(err)
	}

	export := func(opts portability.ExportOptions) []byte {
		t.Helper()
		opts.MediaDir = t.TempDir()
		opts.Secret = commentsTestSecret
		var buf bytes.Buffer
		if _, err := portability.Export(ctx, src, &buf, opts); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	importInto := func(p *pgxpool.Pool, raw []byte) *portability.ImportResult {
		t.Helper()
		res, err := portability.Import(ctx, p, bytes.NewReader(raw), portability.ImportOptions{MediaDir: t.TempDir(), Secret: commentsTestSecret})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	full := export(portability.ExportOptions{})
	res := importInto(dst, full)
	if len(res.Notices) != 0 {
		t.Errorf("same-secret import gave notices: %v", res.Notices)
	}

	// ---- rows identical, sealed emails included
	for _, q := range []string{
		`SELECT coalesce(string_agg(to_jsonb(c)::text, ';' ORDER BY id), '') FROM hatamoto_comments c`,
		`SELECT coalesce(string_agg(to_jsonb(c)::text, ';' ORDER BY id), '') FROM hatamoto_commenters c`,
	} {
		a, b := scalar(src, q), scalar(dst, q)
		if a == "" || a != b {
			t.Errorf("rows differ for %s:\nsrc %s\ndst %s", q, a, b)
		}
	}
	keys := comment.NewKeys(commentsTestSecret)
	rows, err := dst.Query(ctx, `SELECT email_sealed, email_hash FROM hatamoto_commenters`)
	if err != nil {
		t.Fatal(err)
	}
	opened := map[string]bool{}
	for rows.Next() {
		var sealed []byte
		var hash string
		if err := rows.Scan(&sealed, &hash); err != nil {
			t.Fatal(err)
		}
		email, ok := keys.OpenEmail(sealed, hash)
		if !ok {
			t.Fatal("imported email does not open")
		}
		opened[email] = true
	}
	rows.Close()
	if !opened["reader@example.test"] {
		t.Errorf("opened emails = %v, want reader@example.test among them", opened)
	}

	// ---- counts rebuilt, settings untouched, rows reported
	if got := scalar(dst, `SELECT comment_count||','||pending_count FROM hatamoto_comment_entities WHERE entity_id = $1`, post); got != "2,1" {
		t.Errorf("counts = %s, want 2,1", got)
	}
	if got := scalar(dst, `SELECT enabled::text FROM hatamoto_comment_settings`); got != "false" {
		t.Errorf("comments enabled after import = %s, want false", got)
	}
	if res.Rows["hatamoto_comments"] != 5 || res.Rows["hatamoto_commenters"] != 3 {
		t.Errorf("rows = comments %d, commenters %d; want 5 and 3", res.Rows["hatamoto_comments"], res.Rows["hatamoto_commenters"])
	}

	// ---- the sequence moved past the imported rows
	fresh := store(comment.New(dstDB, commentsTestSecret, quietLogger()), post, nil, "new@example.test", "After the import", comment.StatusApproved)
	var freshSeq int64
	if err := dst.QueryRow(ctx, `SELECT seq FROM hatamoto_comments WHERE id = $1`, fresh.ID).Scan(&freshSeq); err != nil {
		t.Fatal(err)
	}
	if freshSeq <= srcMax {
		t.Errorf("seq after import = %d, want > %d", freshSeq, srcMax)
	}

	// ---- --no-submissions leaves comments out
	slim := export(portability.ExportOptions{NoSubmissions: true})
	if bytes.Contains(gunzip(t, slim), []byte("data/hatamoto_comments.jsonl")) {
		t.Error("--no-submissions archive carries comments")
	}
	importInto(bare, slim)
	for _, q := range []string{`SELECT count(*) FROM hatamoto_comments`, `SELECT count(*) FROM hatamoto_commenters`, `SELECT count(*) FROM hatamoto_comment_entities`} {
		if got := scalar(bare, q); got != "0" {
			t.Errorf("--no-submissions import: %s = %s, want 0", q, got)
		}
	}

	// ---- an archive from before comments imports with none
	importInto(older, stripComments(t, full))
	if got := scalar(older, `SELECT count(*) FROM hatamoto_comments`); got != "0" {
		t.Errorf("older archive left %s comments", got)
	}
}

// gunzip returns the decompressed archive bytes (tar headers included).
func gunzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rewriteHeader edits the archive's format.json in place and returns the new archive.
func rewriteHeader(t *testing.T, raw []byte, edit func(map[string]any)) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "format.json" {
			var hdr map[string]any
			if err := jsonx.Unmarshal(b, &hdr); err != nil {
				t.Fatal(err)
			}
			edit(hdr)
			if b, err = jsonx.Marshal(hdr); err != nil {
				t.Fatal(err)
			}
		}
		h.Size = int64(len(b))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// The archive records a check of the server secret (never the secret), a
// different secret is refused unless forced, and forcing blanks the emails.
func TestCommentsArchiveSecret_Integration(t *testing.T) {
	ctx := context.Background()
	srcDSN, src := scratchDB(t)
	if err := migrateUp(ctx, src, quietLogger()); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(srcDSN)
	if err != nil {
		t.Fatal(err)
	}
	srcDB, err := database.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srcDB.Close)
	var post uuid.UUID
	if err := src.QueryRow(ctx, `INSERT INTO hatamoto_entities (schema_id,name,slug,status,published_at)
		SELECT id,'Hello Post','hello-post','published',now() FROM hatamoto_entity_schemas WHERE slug='post' RETURNING id`).Scan(&post); err != nil {
		t.Fatal(err)
	}
	svc := comment.New(srcDB, commentsTestSecret, quietLogger())
	for i, email := range []string{"reader@example.test", "other@example.test"} {
		raw := "Comment " + string(rune('A'+i))
		r, err := commentfmt.Render(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Store(ctx, comment.Draft{EntityID: post, Email: email, AuthorName: "Reader", BodyRaw: raw, BodyHTML: r.HTML,
			BodyHash: r.BodyHash, Status: comment.StatusApproved}); err != nil {
			t.Fatal(err)
		}
	}

	export := func(opts portability.ExportOptions) []byte {
		t.Helper()
		opts.MediaDir = t.TempDir()
		var buf bytes.Buffer
		if _, err := portability.Export(ctx, src, &buf, opts); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	target := func() *pgxpool.Pool {
		t.Helper()
		_, p := scratchDB(t)
		if err := migrateUp(ctx, p, quietLogger()); err != nil {
			t.Fatal(err)
		}
		return p
	}
	count := func(p *pgxpool.Pool, q string) int64 {
		t.Helper()
		var n int64
		if err := p.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	do := func(p *pgxpool.Pool, raw []byte, opts portability.ImportOptions) (*portability.ImportResult, error) {
		opts.MediaDir = t.TempDir()
		return portability.Import(ctx, p, bytes.NewReader(raw), opts)
	}
	hasNotice := func(res *portability.ImportResult, sub string) bool {
		for _, n := range res.Notices {
			if strings.Contains(n, sub) {
				return true
			}
		}
		return false
	}
	full := export(portability.ExportOptions{Secret: commentsTestSecret})

	t.Run("header records the check, not the secret", func(t *testing.T) {
		var hdr portability.Header
		gz, _ := gzip.NewReader(bytes.NewReader(full))
		tr := tar.NewReader(gz)
		h, err := tr.Next()
		if err != nil || h.Name != "format.json" {
			t.Fatalf("first entry = %v, %v", h, err)
		}
		b, _ := io.ReadAll(tr)
		if err := jsonx.Unmarshal(b, &hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Version != 2 || hdr.SecretCheck == "" || hdr.SecretCheck != portability.SecretCheck(commentsTestSecret) {
			t.Errorf("header = version %d, check %q", hdr.Version, hdr.SecretCheck)
		}
		if bytes.Contains(gunzip(t, full), []byte(commentsTestSecret)) {
			t.Error("the archive contains the secret")
		}
	})

	t.Run("different secret is refused", func(t *testing.T) {
		dst := target()
		_, err := do(dst, full, portability.ImportOptions{Secret: "another-secret"})
		if !errors.Is(err, portability.ErrSecretMismatch) || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v", err)
		}
		if n := count(dst, `SELECT count(*) FROM hatamoto_commenters`); n != 0 {
			t.Errorf("refused import left %d commenters", n)
		}
	})

	t.Run("force blanks the emails", func(t *testing.T) {
		dst := target()
		res, err := do(dst, full, portability.ImportOptions{Secret: "another-secret", Force: true})
		if err != nil {
			t.Fatal(err)
		}
		want := count(src, `SELECT count(*) FROM hatamoto_commenters`)
		if want == 0 || res.DroppedEmails != want || !hasNotice(res, "dropped") {
			t.Errorf("dropped %d of %d, notices %v", res.DroppedEmails, want, res.Notices)
		}
		if n := count(dst, `SELECT count(*) FROM hatamoto_commenters WHERE octet_length(email_sealed) <> 0`); n != 0 {
			t.Errorf("%d commenters still have a sealed email", n)
		}
		keys := comment.NewKeys("another-secret")
		rows, err := dst.Query(ctx, `SELECT email_sealed, email_hash FROM hatamoto_commenters`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var sealed []byte
			var hash string
			if err := rows.Scan(&sealed, &hash); err != nil {
				t.Fatal(err)
			}
			if _, ok := keys.OpenEmail(sealed, hash); ok {
				t.Error("a blanked email opens")
			}
		}
		rows.Close()
		if got, w := count(dst, `SELECT count(*) FROM hatamoto_comments`), count(src, `SELECT count(*) FROM hatamoto_comments`); got != w {
			t.Errorf("comments = %d, want %d", got, w)
		}
	})

	t.Run("archive without a check warns", func(t *testing.T) {
		old := rewriteHeader(t, full, func(h map[string]any) { delete(h, "secret_check") })
		res, err := do(target(), old, portability.ImportOptions{Secret: "another-secret"})
		if err != nil {
			t.Fatal(err)
		}
		if !hasNotice(res, "no server secret check") {
			t.Errorf("notices = %v", res.Notices)
		}
	})

	t.Run("archive without comment tables never refuses", func(t *testing.T) {
		slim := export(portability.ExportOptions{Secret: commentsTestSecret, NoSubmissions: true})
		res, err := do(target(), slim, portability.ImportOptions{Secret: "another-secret"})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Notices) != 0 {
			t.Errorf("notices = %v", res.Notices)
		}
	})

	t.Run("no local secret warns", func(t *testing.T) {
		res, err := do(target(), full, portability.ImportOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !hasNotice(res, "not verified") {
			t.Errorf("notices = %v", res.Notices)
		}
	})
}
