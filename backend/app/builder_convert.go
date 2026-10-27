package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

const convertSummary = "Converted to builder doc"

const builderUsage = `  builder convert [--dry-run] [--verify-render] [--entity ID]
                                   move page_sections pages to a page_doc v2 tree (docs/BUILDER.md)`

// convertOpts are the flags of `hatamoto builder convert`.
type convertOpts struct {
	DryRun       bool
	VerifyRender bool
	Entity       string
}

// convertRow is one candidate entity.
type convertRow struct {
	ID      uuid.UUID
	Name    string
	Slug    string
	Attrs   json.RawMessage
	Version int
	// UpdatedAt is read with the row: raw-SQL writers (link rewrites) change
	// the attributes and updated_at but not version.
	UpdatedAt time.Time
}

// convertStore is the database side of the command. Writes go through
// entity.Service.UpdateEntity so a conversion is a version like any other edit.
type convertStore interface {
	// Candidates are entities with page_sections,
	// optionally only the one with id.
	Candidates(ctx context.Context, id string) ([]convertRow, error)
	// Stamp is the entity's current version and updated_at, read right before a write.
	Stamp(ctx context.Context, id uuid.UUID) (int, time.Time, error)
	Update(ctx context.Context, id uuid.UUID, in *entity.UpdateEntityInput) error
}

// pageRenderer is the part of the render engine the verification needs.
type pageRenderer interface {
	RenderPage(w io.Writer, v *render.PageView) error
}

// runBuilderConvert parses the flags and runs the conversion against databaseURL.
func runBuilderConvert(ctx context.Context, databaseURL string, args []string, out io.Writer) error {
	var o convertOpts
	var revert bool
	pos, err := flagSet("builder convert", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&o.DryRun, "dry-run", false, "report what would change, write nothing")
		fs.BoolVar(&o.VerifyRender, "verify-render", false, "render both shapes and compare the bytes (always done before a write)")
		fs.BoolVar(&revert, "revert", false, "unsupported: the page_sections mirror is no longer maintained")
		fs.StringVar(&o.Entity, "entity", "", "convert only this entity id")
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("usage: hatamoto builder convert [--dry-run] [--verify-render] [--entity ID]")
	}
	if revert {
		return errors.New("unsupported: the page_sections mirror is no longer maintained")
	}
	if o.Entity != "" {
		if _, err := uuid.Parse(o.Entity); err != nil {
			return fmt.Errorf("--entity: not a uuid: %q", o.Entity)
		}
	}
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Database.URL = databaseURL
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The default theme verifies the shapes against each other; the active
	// theme package is not loaded by the CLI (docs/BUILDER.md).
	eng, err := render.New(render.Options{Logger: quiet})
	if err != nil {
		return err
	}
	svc := entity.NewService(entity.NewPostgresSchemaRepository(db), entity.NewPostgresEntityRepository(db), quiet)
	return convertPages(ctx, &pgConvertStore{db: db, svc: svc}, eng, o, out)
}

// logUnconvertedPages is the read-only boot hint: pages that still have only
// page_sections open in the builder through lazy conversion, and
// `hatamoto builder convert` converts them all at once.
func logUnconvertedPages(ctx context.Context, pool queryRower, logger *slog.Logger) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities
		WHERE jsonb_typeof(attributes->'page_sections') = 'array' AND NOT attributes ? 'page_doc'`).Scan(&n)
	if err != nil {
		logger.Warn("cannot count pages without a page_doc", "error", err)
		return
	}
	if n > 0 {
		logger.Info("pages without a page_doc", "count", n,
			"hint", "run `hatamoto builder convert` to convert them (docs/BUILDER.md)")
	}
}

// queryRower is the part of *pgxpool.Pool the boot count needs.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type pgConvertStore struct {
	db  *database.DB
	svc *entity.Service
}

func (s *pgConvertStore) Candidates(ctx context.Context, id string) ([]convertRow, error) {
	// Already-v2 pages stay in the convert list so they are counted and skipped.
	cond := `jsonb_typeof(attributes->'page_sections') = 'array'`
	q := `SELECT id, name, slug, attributes, version, updated_at FROM hatamoto_entities WHERE ` + cond
	args := []any{}
	if id != "" {
		q += ` AND id = $1`
		args = append(args, id)
	}
	rows, err := s.db.Pool().Query(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []convertRow
	for rows.Next() {
		var r convertRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Slug, &r.Attrs, &r.Version, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *pgConvertStore) Stamp(ctx context.Context, id uuid.UUID) (int, time.Time, error) {
	var v int
	var at time.Time
	err := s.db.Pool().QueryRow(ctx, `SELECT version, updated_at FROM hatamoto_entities WHERE id = $1`, id).Scan(&v, &at)
	return v, at, err
}

func (s *pgConvertStore) Update(ctx context.Context, id uuid.UUID, in *entity.UpdateEntityInput) error {
	_, err := s.svc.UpdateEntity(ctx, id, nil, in)
	return err
}

// convertStats is the summary line.
type convertStats struct{ Pages, Convertible, Identical, Diffs, Skipped, Failed int }

// convertPages converts every candidate, printing one row each and
// a summary. It returns an error when any page rendered differently or failed,
// so the process exits non-zero.
func convertPages(ctx context.Context, st convertStore, eng pageRenderer, o convertOpts, out io.Writer) error {
	rows, err := st.Candidates(ctx, o.Entity)
	if err != nil {
		return err
	}
	var s convertStats
	for _, r := range rows {
		s.Pages++
		res := convertOne(ctx, st, eng, o, r, &s)
		fmt.Fprintf(out, "%s\t%s\t%s\n", r.ID, r.Slug, res)
	}
	fmt.Fprintf(out, "pages: %d convertible: %d render-identical: %d diffs: %d skipped (already v2): %d\n",
		s.Pages, s.Convertible, s.Identical, s.Diffs, s.Skipped)
	switch {
	case s.Diffs > 0:
		return fmt.Errorf("%d page(s) render differently as a builder doc; nothing was written for them", s.Diffs)
	case s.Failed > 0:
		return fmt.Errorf("%d page(s) failed", s.Failed)
	}
	return nil
}

func convertOne(ctx context.Context, st convertStore, eng pageRenderer, o convertOpts, r convertRow, s *convertStats) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(r.Attrs, &raw); err != nil {
		s.Failed++
		return "error: attributes are not an object"
	}
	_, hasDoc := raw["page_doc"]

	var sections []builder.Section
	var doc *builder.Doc
	var attrs json.RawMessage // convert: the attributes to write
	var err error
	if hasDoc {
		s.Skipped++
		return "skipped (already v2)"
	}
	if sections, err = entity.SectionsFromAttributes(r.Attrs); err != nil {
		s.Failed++
		return "error: " + err.Error()
	}
	// Verify what will be stored: the doc after the write seam's own
	// sanitising and validation, not the raw conversion.
	if attrs, doc, err = normalizedConvert(r.Attrs, raw, sections); err != nil {
		s.Failed++
		return "error: " + err.Error()
	}

	s.Convertible++

	verdict := "not verified"
	if o.VerifyRender || !o.DryRun {
		same, err := renderIdentical(eng, r, doc)
		if err != nil {
			s.Failed++
			return "error: render: " + err.Error()
		}
		if !same {
			s.Diffs++
			return "DIFF: render differs, not written"
		}
		s.Identical++
		verdict = "render identical"
	}
	if o.DryRun {
		return verdict + " (dry run)"
	}

	// The candidate list was read a while ago: an editor may have saved since.
	// Everything above was computed from that read, so writing now would
	// overwrite the edit; leave the page for the next run instead.
	if cur, at, err := st.Stamp(ctx, r.ID); err != nil {
		s.Failed++
		return "error: " + err.Error()
	} else if cur != r.Version || !at.Equal(r.UpdatedAt) {
		s.Failed++
		return "error: changed since it was read; not written, run again"
	}

	// A lossless shape change is not an edit: the page keeps its modified date.
	in := &entity.UpdateEntityInput{KeepUpdatedAt: true, Attributes: &attrs, ChangeSummary: ptr(convertSummary)}
	if err := st.Update(ctx, r.ID, in); err != nil {
		s.Failed++
		return "error: " + err.Error()
	}
	return "converted"
}

// normalizedConvert adds the converted doc to the page's attributes and runs the
// write seam over them, returning the attributes to write and the doc inside.
func normalizedConvert(stored json.RawMessage, raw map[string]json.RawMessage, sections []builder.Section) (json.RawMessage, *builder.Doc, error) {
	docJSON, err := json.Marshal(builder.FromSections(sections))
	if err != nil {
		return nil, nil, err
	}
	next := make(map[string]json.RawMessage, len(raw)+1)
	for k, v := range raw {
		next[k] = v
	}
	next["page_doc"] = docJSON
	b, err := json.Marshal(next)
	if err != nil {
		return nil, nil, err
	}
	attrs, err := entity.NormalizeAttributes(stored, b)
	if err != nil {
		return nil, nil, err
	}
	var out map[string]json.RawMessage // exact key: a struct tag matches "Page_Doc" too
	if err := json.Unmarshal(attrs, &out); err != nil {
		return nil, nil, err
	}
	doc, err := builder.Parse(out["page_doc"])
	if err != nil {
		return nil, nil, err
	}
	return attrs, doc, nil
}

func ptr[T any](v T) *T { return &v }

// renderIdentical renders the page from its stored page_sections and from doc with the same
// view and compares the bytes.
func renderIdentical(eng pageRenderer, r convertRow, doc *builder.Doc) (bool, error) {
	view := func() *render.PageView {
		return &render.PageView{
			Kind: render.KindPage, Path: "/", EntityID: r.ID.String(),
			Page: render.PageMeta{Name: r.Name, Title: r.Name},
		}
	}
	// Shape A is what the public renderer reads today: same reader, plain float64.
	a := view()
	for _, s := range builder.SectionsFromJSON(r.Attrs, false) {
		a.Sections = append(a.Sections, render.Section{ID: s.ID, Type: s.Type, Title: s.Title, Content: s.Content, IsVisible: s.IsVisible, SortOrder: s.SortOrder})
	}
	b := view()
	b.Doc = doc
	var ba, bb bytes.Buffer
	if err := eng.RenderPage(&ba, a); err != nil {
		return false, err
	}
	if err := eng.RenderPage(&bb, b); err != nil {
		return false, err
	}
	return bytes.Equal(ba.Bytes(), bb.Bytes()), nil
}
