package portability

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/person"
)

// ErrNotEmpty is returned when the target database already has content.
var ErrNotEmpty = errors.New("database is not empty")

// ImportOptions control Import.
type ImportOptions struct {
	// MediaDir is the upload directory media files are written to.
	MediaDir string
	// MaxBytes caps the decompressed archive (a gzip-bomb guard). Zero means
	// DefaultMaxImportBytes.
	MaxBytes int64
}

// DefaultMaxImportBytes is the decompressed-size cap when none is set.
const DefaultMaxImportBytes = 64 << 30

// maxRowBytes caps one JSON line of a data file.
const maxRowBytes = 64 << 20

// ImportResult says what was imported.
type ImportResult struct {
	Header     Header
	Rows       map[string]int64
	MediaFiles int
	MediaBytes int64
	Users      int64
	// Warnings lists media files the import skipped (per-file, not fatal).
	Warnings []string
	// People is how many person entities the post-import backfill made for an
	// archive that predates people (zero for an archive that has them).
	People int
	// needPeople is set inside the import transaction: authors but no people.
	needPeople bool
}

// contentTables must be empty before an import. The entity schemas and the
// site config are not on the list: migrations seed them, and the import
// replaces them.
var contentTables = []string{
	"hatamoto_entities", "hatamoto_authors", "hatamoto_user_people", "hatamoto_users", "hatamoto_terms", "hatamoto_menus", "hatamoto_redirects",
	"hatamoto_forms", "hatamoto_media", "hatamoto_section_templates", "hatamoto_internal_link_rules",
}

const insertBatch = 200

// Import reads an archive from r into an empty, migrated database, keeping
// ids and timestamps. It runs in one transaction: any error, including a
// failed manifest check, leaves the database as it was. Media files are staged
// in a temp directory under MediaDir and linked into place just before the
// commit; a failure removes only the files this import put there, never one
// that was already in MediaDir (an import refuses to overwrite). Users come
// back without passwords.
//
// An archive from before people existed has authors and no person entities:
// once it is committed, person.Backfill makes the people (it is idempotent, so
// running it again after a failure is safe). No FormatVersion bump: the
// archive layout is unchanged, hatamoto_user_people is just one more table
// file, and archives without it import as before.
func Import(ctx context.Context, pool *pgxpool.Pool, r io.Reader, opts ImportOptions) (*ImportResult, error) {
	res, err := importArchive(ctx, pool, r, opts)
	if err != nil || !res.needPeople {
		return res, err
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("the archive was imported, but making its people failed: %w", err)
	}
	defer tx.Rollback()
	if err := person.Backfill(ctx, tx); err != nil {
		return res, fmt.Errorf("the archive was imported, but making its people failed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("the archive was imported, but making its people failed: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person'`).Scan(&res.People); err != nil {
		return res, err
	}
	return res, nil
}

func importArchive(ctx context.Context, pool *pgxpool.Pool, r io.Reader, opts ImportOptions) (res *ImportResult, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxImportBytes
	}
	tr := tar.NewReader(io.LimitReader(gz, maxBytes))

	hdr, err := readHeader(tr)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	stage := &mediaStage{root: opts.MediaDir}
	defer func() {
		stage.cleanup()
		if err != nil {
			_ = tx.Rollback(ctx)
			stage.undo()
		}
	}()

	if err := requireEmpty(ctx, tx); err != nil {
		return nil, err
	}
	if err := requireSchemaNotNewer(ctx, tx, hdr); err != nil {
		return nil, err
	}
	// Rows go in without their parent (it is set afterwards), so siblings that
	// share a slug would collide in the slug indexes. They are dropped here and
	// rebuilt once the parents are linked.
	slugIndexes, err := dropSlugIndexes(ctx, tx)
	if err != nil {
		return nil, err
	}
	// Migrations seed default schemas and a site config; the archive carries
	// the site's own.
	// An archive from before people has no person type: keep the built-in one.
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE hm_person_schema ON COMMIT DROP AS
		SELECT * FROM hatamoto_entity_schemas WHERE slug = 'person' AND is_builtin`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_entity_schemas; DELETE FROM hatamoto_site_config`); err != nil {
		return nil, err
	}
	// The updated_at triggers would overwrite imported timestamps when the
	// parent links are set below.
	for _, t := range tables {
		if t.selfRef != "" {
			if _, err := tx.Exec(ctx, "ALTER TABLE public."+t.name+" DISABLE TRIGGER USER"); err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE hm_parents (tbl text, id uuid, parent uuid) ON COMMIT DROP`); err != nil {
		return nil, err
	}

	res = &ImportResult{Header: *hdr, Rows: map[string]int64{}}
	var man *Manifest
	next := 0 // index into hdr.Tables: data files must follow the header's order
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		switch {
		case man != nil:
			return nil, fmt.Errorf("unexpected entry %q after the manifest", h.Name)
		case h.Name == manifestEntry:
			man = &Manifest{}
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(man); err != nil {
				return nil, fmt.Errorf("manifest: %w", err)
			}
		case strings.HasPrefix(h.Name, dataPrefix) && strings.HasSuffix(h.Name, dataSuffix):
			name := strings.TrimSuffix(strings.TrimPrefix(h.Name, dataPrefix), dataSuffix)
			t, ok := tableByName(name)
			if !ok || next >= len(hdr.Tables) || hdr.Tables[next] != name {
				return nil, fmt.Errorf("unexpected data file %q", h.Name)
			}
			next++
			n, err := importTable(ctx, tx, t, tr)
			if err != nil {
				return nil, fmt.Errorf("import %s: %w", name, err)
			}
			res.Rows[name] = n
		case strings.HasPrefix(h.Name, mediaPrefix):
			if h.Typeflag != tar.TypeReg {
				return nil, fmt.Errorf("media entry %q is not a regular file", h.Name)
			}
			n, warn, err := stage.add(strings.TrimPrefix(h.Name, mediaPrefix), tr)
			if err != nil {
				return nil, err
			}
			if warn != "" {
				res.Warnings = append(res.Warnings, warn)
			}
			res.MediaFiles++
			res.MediaBytes += n
		default:
			return nil, fmt.Errorf("unexpected archive entry %q", h.Name)
		}
	}
	if man == nil {
		return nil, errors.New("archive has no manifest: it is incomplete")
	}
	if next != len(hdr.Tables) {
		return nil, errors.New("archive is missing data files")
	}
	if err := checkManifest(man, res); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_entity_schemas SELECT * FROM hm_person_schema
		WHERE NOT EXISTS (SELECT 1 FROM hatamoto_entity_schemas WHERE slug = 'person')`); err != nil {
		return nil, fmt.Errorf("restore the person type: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hatamoto_authors)
		AND NOT EXISTS (SELECT 1 FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON s.id = e.schema_id AND s.slug = 'person')`).Scan(&res.needPeople); err != nil {
		return nil, err
	}

	if err := linkParents(ctx, tx); err != nil {
		return nil, err
	}
	// The slug_nested trigger was off while rows went in; an archive from before
	// that column existed leaves every row flat. Settle the flags now, still
	// with the triggers off so updated_at is kept.
	if _, err := tx.Exec(ctx, `
		UPDATE hatamoto_entities e SET slug_nested = public.entity_schema_nested(s.settings)
		FROM hatamoto_entity_schemas s
		WHERE s.id = e.schema_id AND e.slug_nested IS DISTINCT FROM public.entity_schema_nested(s.settings)`); err != nil {
		return nil, fmt.Errorf("settle slug scope: %w", err)
	}
	for _, def := range slugIndexes {
		if _, err := tx.Exec(ctx, def); err != nil {
			return nil, fmt.Errorf("the archive has entities that share a slug: %w", err)
		}
	}
	for _, t := range tables {
		if t.selfRef != "" {
			if _, err := tx.Exec(ctx, "ALTER TABLE public."+t.name+" ENABLE TRIGGER USER"); err != nil {
				return nil, err
			}
		}
	}
	res.Users = res.Rows["hatamoto_users"]
	if err := stage.place(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func readHeader(tr *tar.Reader) (*Header, error) {
	h, err := tr.Next()
	if err != nil || h.Name != formatEntry {
		return nil, errors.New("not a hatamoto export: format.json must be the first entry")
	}
	var hdr Header
	if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&hdr); err != nil {
		return nil, fmt.Errorf("format.json: %w", err)
	}
	if hdr.Format != FormatName {
		return nil, fmt.Errorf("not a hatamoto export (format %q)", hdr.Format)
	}
	if hdr.Version < 1 || hdr.Version > FormatVersion {
		return nil, fmt.Errorf("archive format version %d is not supported (this binary reads up to %d)", hdr.Version, FormatVersion)
	}
	for _, name := range hdr.Tables {
		if _, ok := tableByName(name); !ok {
			return nil, fmt.Errorf("archive lists unknown table %q", name)
		}
	}
	return &hdr, nil
}

// requireSchemaNotNewer refuses an archive from a newer Hatamoto: columns this
// database does not know would be dropped silently.
func requireSchemaNotNewer(ctx context.Context, tx pgx.Tx, hdr *Header) error {
	var have int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&have); err != nil {
		return fmt.Errorf("read schema version (is the database migrated?): %w", err)
	}
	if hdr.SchemaVersion > have {
		return fmt.Errorf("archive comes from a newer Hatamoto (schema version %d, this database is at %d): upgrade the binary and migrate first", hdr.SchemaVersion, have)
	}
	return nil
}

func requireEmpty(ctx context.Context, tx pgx.Tx) error {
	for _, name := range contentTables {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		var n int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM public."+name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s has %d rows; import only into a freshly migrated database", ErrNotEmpty, name, n)
		}
	}
	return nil
}

func checkManifest(m *Manifest, res *ImportResult) error {
	for name, want := range m.Rows {
		if got := res.Rows[name]; got != want {
			return fmt.Errorf("manifest says %s has %d rows, archive held %d", name, want, got)
		}
	}
	if len(m.Rows) != len(res.Rows) {
		return errors.New("manifest and data files disagree on the tables")
	}
	if m.MediaFiles != res.MediaFiles {
		return fmt.Errorf("manifest says %d media files, archive held %d", m.MediaFiles, res.MediaFiles)
	}
	if m.MediaBytes != res.MediaBytes {
		return fmt.Errorf("manifest says %d media bytes, archive held %d", m.MediaBytes, res.MediaBytes)
	}
	return nil
}

// importTable inserts every JSON line of r into t, in batches.
func importTable(ctx context.Context, tx pgx.Tx, t table, r io.Reader) (int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var (
		n     int64
		query string
		batch = &pgx.Batch{}
	)
	flush := func() error {
		if batch.Len() == 0 {
			return nil
		}
		res := tx.SendBatch(ctx, batch)
		for range batch.Len() {
			if _, err := res.Exec(); err != nil {
				res.Close()
				return err
			}
		}
		batch = &pgx.Batch{}
		return res.Close()
	}
	for {
		line, err := readLine(br)
		if len(strings.TrimSpace(string(line))) > 0 {
			if query == "" {
				if query, err = insertQuery(ctx, tx, t, line); err != nil {
					return 0, err
				}
			}
			row, perr := prepareRow(t, line)
			if perr != nil {
				return 0, perr
			}
			batch.Queue(query, row)
			n++
			if batch.Len() >= insertBatch {
				if err := flush(); err != nil {
					return 0, err
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	return n, flush()
}

// readLine reads one line of at most maxRowBytes.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := br.ReadSlice('\n')
		line = append(line, frag...)
		if len(line) > maxRowBytes {
			return nil, fmt.Errorf("a data row is longer than %d bytes", maxRowBytes)
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

// prepareRow applies the table's import rules to a row: the self-reference is
// stashed and cleared, the forced values are merged.
func prepareRow(t table, line []byte) (string, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return "", fmt.Errorf("bad row: %w", err)
	}
	if t.name == "hatamoto_media" {
		if err := checkMediaRow(line); err != nil {
			return "", err
		}
	}
	if t.forced != "" {
		var o map[string]json.RawMessage
		_ = json.Unmarshal([]byte(t.forced), &o)
		for k, v := range o {
			m[k] = v
		}
	}
	if t.selfRef != "" {
		if p := m[t.selfRef]; len(p) > 0 && string(p) != "null" {
			// recorded by linkParents' second pass through the same row
			m["__parent"] = p
		}
		delete(m, t.selfRef)
	}
	b, err := json.Marshal(m)
	return string(b), err
}

// insertQuery builds the INSERT for t from the columns present in the first
// row (so an archive from an older version, with fewer columns, still loads
// and the missing ones take their defaults).
func insertQuery(ctx context.Context, tx pgx.Tx, t table, firstLine []byte) (string, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(firstLine, &keys); err != nil {
		return "", fmt.Errorf("bad row: %w", err)
	}
	if t.forced != "" {
		var o map[string]json.RawMessage
		_ = json.Unmarshal([]byte(t.forced), &o)
		for k, v := range o {
			keys[k] = v
		}
	}
	rows, err := tx.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND is_generated = 'NEVER' ORDER BY ordinal_position`, t.name)
	if err != nil {
		return "", err
	}
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return "", err
		}
		_, present := keys[c]
		if present && c != t.selfRef {
			cols = append(cols, pgx.Identifier{c}.Sanitize())
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("no importable columns for %s", t.name)
	}
	list := strings.Join(cols, ", ")
	tbl := pgx.Identifier{"public", t.name}.Sanitize()
	q := fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM jsonb_populate_record(NULL::%s, $1::jsonb)`, tbl, list, list, tbl)
	if t.selfRef == "" {
		return q, nil
	}
	// Self-referencing tables also record (id, parent) for the second pass.
	return fmt.Sprintf(`WITH ins AS (%s RETURNING id)
		INSERT INTO hm_parents SELECT %s, ($1::jsonb->>'id')::uuid, ($1::jsonb->>'__parent')::uuid
		FROM ins WHERE $1::jsonb->>'__parent' IS NOT NULL`, q, quoteLit(t.name)), nil
}

func quoteLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// linkParents sets the self-references recorded during the inserts.
func linkParents(ctx context.Context, tx pgx.Tx) error {
	for _, t := range tables {
		if t.selfRef == "" {
			continue
		}
		q := fmt.Sprintf(`UPDATE public.%s x SET %s = p.parent FROM hm_parents p WHERE p.tbl = %s AND x.id = p.id`,
			t.name, t.selfRef, quoteLit(t.name))
		if _, err := tx.Exec(ctx, q); err != nil {
			return fmt.Errorf("link %s parents: %w", t.name, err)
		}
	}
	return nil
}

// checkMediaRow refuses a media row whose files would live outside the upload
// directory: a later delete or export acts on those paths, so an archive must
// not be able to name any it cannot name legitimately.
func checkMediaRow(line []byte) error {
	var m struct {
		Provider *string `json:"storage_provider"`
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return fmt.Errorf("bad media row: %w", err)
	}
	if m.Provider != nil && *m.Provider != "local" {
		return fmt.Errorf("media row uses storage provider %q; only local storage can be imported", *m.Provider)
	}
	paths, err := mediaPathsOf(string(line))
	if err != nil {
		return fmt.Errorf("bad media row: %w", err)
	}
	for _, p := range paths {
		if _, err := cleanMediaPath(p); err != nil {
			return fmt.Errorf("media row: %w", err)
		}
	}
	return nil
}

// mediaStage collects the archive's media files in a temp directory under the
// upload directory, then links them into place (place), so a failed import
// never touches files that were already there.
type mediaStage struct {
	root   string
	dir    string   // staging dir, created on the first file
	staged []string // clean relative paths
	placed []string // absolute paths this import created
}

// add stages one file and returns its size. Entry names are cleaned: absolute
// paths, "..", and files that already exist in the upload directory are refused.
// A file whose type is not allowed (an old .svg or .txt upload from before
// uploads were sniffed) is skipped, not staged: the size is still returned so
// the manifest check holds, and warn says why.
func (m *mediaStage) add(rel string, r io.Reader) (n int64, warn string, err error) {
	clean, err := cleanMediaPath(rel)
	if err != nil {
		return 0, "", err
	}
	// Same rule as an upload: the sniffed type must be an allowed one and agree
	// with the extension, so an archive cannot plant .html or script that the
	// site would serve from its own origin. Two allowed image types that do not
	// pair (JPEG bytes named .webp, written by older versions) are accepted.
	br := bufio.NewReaderSize(r, 512)
	head, _ := br.Peek(512)
	ext := filepath.Ext(clean)
	if sniffed := http.DetectContentType(head); !media.ExtensionMatchesMIME(ext, sniffed) && !media.ImageTypeAndExtensionAllowed(ext, sniffed) {
		n, err := io.Copy(io.Discard, br)
		if err != nil {
			return 0, "", err
		}
		return n, fmt.Sprintf("skipped media file %q (%s): not an allowed media type", clean, sniffed), nil
	}
	r = br
	if _, err := os.Lstat(filepath.Join(m.root, filepath.FromSlash(clean))); err == nil {
		return 0, "", fmt.Errorf("media file %q already exists in %s; import into an empty media directory", clean, m.root)
	}
	if m.dir == "" {
		if err := os.MkdirAll(m.root, 0o755); err != nil {
			return 0, "", err
		}
		if m.dir, err = os.MkdirTemp(m.root, ".import-"); err != nil {
			return 0, "", err
		}
	}
	full := filepath.Join(m.dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return 0, "", err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, "", fmt.Errorf("media entry %q appears twice or cannot be written: %w", clean, err)
	}
	n, cerr := io.Copy(f, r)
	if err := f.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return 0, "", cerr
	}
	m.staged = append(m.staged, clean)
	return n, "", nil
}

// place links every staged file to its final path without overwriting, after
// checking that no symlink in the way leads out of the upload directory.
func (m *mediaStage) place() error {
	if len(m.staged) == 0 {
		return nil
	}
	root, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return err
	}
	for _, clean := range m.staged {
		dest := filepath.Join(m.root, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(filepath.Dir(dest))
		if err != nil {
			return err
		}
		if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
			return fmt.Errorf("unsafe media path %q: it resolves outside the media directory", clean)
		}
		if err := os.Link(filepath.Join(m.dir, filepath.FromSlash(clean)), dest); err != nil {
			return fmt.Errorf("place media file %q: %w", clean, err)
		}
		m.placed = append(m.placed, dest)
	}
	return nil
}

// undo removes the files place created (and nothing else).
func (m *mediaStage) undo() {
	for _, p := range m.placed {
		_ = os.Remove(p)
	}
}

// cleanup deletes the staging directory.
func (m *mediaStage) cleanup() {
	if m.dir != "" {
		_ = os.RemoveAll(m.dir)
	}
}

// slugIndexNames are the unique slug indexes of hatamoto_entities.
var slugIndexNames = []string{"uq_entities_schema_slug", "uq_entities_schema_parent_slug"}

// dropSlugIndexes drops the unique slug indexes and returns their definitions.
func dropSlugIndexes(ctx context.Context, tx pgx.Tx) ([]string, error) {
	var defs []string
	for _, name := range slugIndexNames {
		var def string
		err := tx.QueryRow(ctx, `SELECT pg_get_indexdef(c.oid) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relname = $1`, name).Scan(&def)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "DROP INDEX public."+name); err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}
