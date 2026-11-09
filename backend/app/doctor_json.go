package app

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/consent"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/installstats"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/jsonx/keynorm"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/seo"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/internal/tags"
)

const doctorUsage = `  doctor json                      verify stored JSON decodes strictly and report case-variant keys (read-only)
                                   exit 0 all rows ok, 1 some rows fail, 2 could not connect or query`

// colCheck is how the doctor reads one JSON column. A nil decode only checks
// that the value is valid JSON; a nil shape skips the case-variant key check.
type colCheck struct {
	decode func([]byte) error
	shape  *keynorm.Shape
}

func decodeAs[T any]() func([]byte) error {
	return func(raw []byte) error {
		var v T
		return jsonx.Unmarshal(raw, &v, jsonx.Stored)
	}
}

// opaque is a column the application reads without a fixed Go type (plugin
// data, snapshots, schemas, content trees) or one a library owns. Typed
// columns are registered with decodeAs instead.
var opaque = colCheck{}

// doctorRegistry maps "table.column" to its check. Every JSON column of a
// migrated database must have an entry (TestDoctorRegistryCoversEveryJSONColumn
// fails otherwise), so a new column is registered when it is added. The four
// columns the key-normalising migration rewrites carry a shape and so do
// locales and media.variants, where the doctor only reports case variants
// (nothing rewrites them: a variant there is dropped by a strict decode).
var doctorRegistry = map[string]colCheck{
	"hatamoto_site_config.theme_extra":             {decode: decodeAs[siteconfig.ThemeConfig](), shape: themeExtraShape()},
	"hatamoto_entity_schemas.settings":             {decode: decodeAs[entity.SchemaURLSettings](), shape: keynorm.ShapeOf(reflect.TypeFor[entity.SchemaURLSettings]())},
	"hatamoto_forms.fields":                        {decode: decodeAs[[]form.FormField](), shape: keynorm.ShapeOf(reflect.TypeFor[[]form.FormField]())},
	"hatamoto_forms.settings":                      {decode: decodeAs[form.FormSettings](), shape: keynorm.ShapeOf(reflect.TypeFor[form.FormSettings]())},
	"hatamoto_site_config.locales":                 {decode: decodeAs[[]siteconfig.LocaleEntry](), shape: keynorm.ShapeOf(reflect.TypeFor[[]siteconfig.LocaleEntry]())},
	"hatamoto_media.variants":                      {decode: decodeAs[[]media.ImageVariant](), shape: keynorm.ShapeOf(reflect.TypeFor[[]media.ImageVariant]())},
	"hatamoto_api_keys.scopes":                     {decode: decodeAs[[]string]()},
	"hatamoto_webhooks.events":                     {decode: decodeAs[[]string]()},
	"hatamoto_webhooks.headers":                    {decode: decodeAs[map[string]string]()},
	"hatamoto_admin_audit.detail":                  opaque,
	"hatamoto_changeset_items.before":              opaque,
	"hatamoto_changeset_items.payload":             opaque,
	"hatamoto_entities.attributes":                 opaque,
	"hatamoto_entities.editorial":                  opaque,
	"hatamoto_entities.metadata":                   opaque,
	"hatamoto_entity_schemas.schema":               opaque,
	"hatamoto_entity_schemas.ui_schema":            opaque,
	"hatamoto_entity_versions.attributes":          opaque,
	"hatamoto_form_submissions.data":               opaque,
	"hatamoto_form_submissions.metadata":           {decode: decodeAs[form.SubmissionMetadata]()},
	"hatamoto_import_runs.options":                 opaque,
	"hatamoto_import_runs.report":                  opaque,
	"hatamoto_locale_conversion_rows.before_attrs": opaque,
	"hatamoto_locale_conversions.plan":             opaque,
	"hatamoto_locale_conversions.report":           opaque,
	"hatamoto_locale_conversions.schemas_after":    opaque,
	"hatamoto_locale_conversions.schemas_before":   opaque,
	"hatamoto_locale_conversions.settings_after":   opaque,
	"hatamoto_locale_conversions.settings_before":  opaque,
	"hatamoto_install_stats.h_plugin_errors":       {decode: decodeAs[map[string]int64]()},
	"hatamoto_install_stats.h_sent":                {decode: decodeAs[installstats.Counts]()},
	"hatamoto_mcp_audit.args":                      opaque,
	"hatamoto_menu_items.attrs":                    opaque,
	"hatamoto_migration_notes.details":             opaque,
	"hatamoto_packages.manifest":                   opaque,
	"hatamoto_plugin_handover.value":               opaque,
	"hatamoto_plugin_kv.value":                     opaque,
	"hatamoto_plugin_settings.settings":            opaque,
	"hatamoto_privacy_settings.settings":           {decode: decodeAs[consent.Settings]()},
	"hatamoto_run_proposals.before":                opaque,
	"hatamoto_run_proposals.payload":               opaque,
	"hatamoto_runs.params":                         opaque,
	"hatamoto_runs.progress":                       opaque,
	"hatamoto_runs.summary":                        opaque,
	"hatamoto_section_templates.content":           opaque,
	"hatamoto_seo_settings.settings":               {decode: decodeAs[seo.Settings]()},
	"hatamoto_site_config.business_profile":        {decode: decodeAs[bizprofile.Profile]()},
	"hatamoto_site_config.locale_profiles":         {decode: decodeAs[map[string]bizprofile.Profile]()},
	"hatamoto_site_config.postal_address":          {decode: decodeAs[bizprofile.Address]()},
	"hatamoto_standing_approvals.fingerprint":      opaque,
	"hatamoto_standing_approvals.policy":           opaque,
	"hatamoto_terms.seo":                           opaque,
	"hatamoto_theme_settings.settings":             opaque,
	"hatamoto_tracking_settings.settings":          {decode: decodeAs[tags.Settings]()},
	"hatamoto_update_settings.first_seen":          opaque,
	// Library-owned (go-webauthn writes and reads them): validity only.
	"hatamoto_user_passkeys.credential": opaque,
	"hatamoto_webauthn_ceremonies.data": opaque,
}

// themeExtraShape is the key layout of hatamoto_site_config.theme_extra: the
// typed theme plus the extras stored next to it.
func themeExtraShape() *keynorm.Shape {
	s := keynorm.ShapeOf(reflect.TypeFor[siteconfig.ThemeConfig]())
	if s == nil { // ThemeConfig gained its own UnmarshalJSON: only the extras remain
		s = &keynorm.Shape{Fields: map[string]*keynorm.Shape{}}
	}
	s.Fields["socialLinks"] = &keynorm.Shape{Open: true}
	s.Fields["builder"] = nil
	s.Fields["media_formats"] = keynorm.ShapeOf(reflect.TypeFor[struct {
		WebP bool `json:"webp"`
		AVIF bool `json:"avif"`
	}]())
	s.Fields["media_rebuild"] = nil
	return s
}

// jsonColumn names one JSON column and how to identify its rows.
type jsonColumn struct {
	Table, Column string
	HasID         bool // rows are identified by id; otherwise by ctid
}

func (c jsonColumn) key() string { return c.Table + "." + c.Column }

// jsonSource is the read side of the doctor.
type jsonSource interface {
	Columns(ctx context.Context) ([]jsonColumn, error)
	// Rows calls fn with the id and the JSON text of every non-NULL value.
	Rows(ctx context.Context, c jsonColumn, fn func(id string, raw []byte) error) error
}

type rowFailure struct {
	ID     string
	Reason string
}

type columnReport struct {
	Key          string
	Rows         int
	Unregistered bool
	Failures     []rowFailure
}

// jsonReport is the outcome of one doctor run.
type jsonReport struct {
	Columns     []columnReport
	DecodeFails int // rows whose Stored decode failed
	VariantKeys int // case-variant keys found
	FailedRows  int
}

// checkStoredJSON reads every JSON column of src. It only reads.
func checkStoredJSON(ctx context.Context, src jsonSource) (*jsonReport, error) {
	cols, err := src.Columns(ctx)
	if err != nil {
		return nil, err
	}
	rep := &jsonReport{}
	for _, c := range cols {
		chk, known := doctorRegistry[c.key()]
		cr := columnReport{Key: c.key(), Unregistered: !known}
		err := src.Rows(ctx, c, func(id string, raw []byte) error {
			cr.Rows++
			var reasons []string
			if !jsontext.Value(raw).IsValid() {
				reasons = append(reasons, "not valid JSON")
			} else {
				if chk.decode != nil {
					if err := chk.decode(raw); err != nil {
						rep.DecodeFails++
						reasons = append(reasons, "decode: "+oneLine(err.Error()))
					}
				}
				if chk.shape != nil {
					_, changes, err := keynorm.Normalise(raw, chk.shape)
					if err != nil {
						reasons = append(reasons, "keys: "+oneLine(err.Error()))
					}
					if len(changes) > 0 {
						rep.VariantKeys += len(changes)
						parts := make([]string, len(changes))
						for i, ch := range changes {
							parts[i] = ch.String()
						}
						reasons = append(reasons, "case-variant keys: "+strings.Join(parts, ", "))
					}
				}
			}
			if len(reasons) > 0 {
				cr.Failures = append(cr.Failures, rowFailure{ID: id, Reason: strings.Join(reasons, "; ")})
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.key(), err)
		}
		rep.FailedRows += len(cr.Failures)
		rep.Columns = append(rep.Columns, cr)
	}
	return rep, nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// render writes the report. The last line is OK or FAILED.
func (r *jsonReport) render(w io.Writer) {
	width := 0
	for _, c := range r.Columns {
		width = max(width, len(c.Key))
	}
	fmt.Fprintln(w, "stored JSON vs strict v2 decode")
	for _, c := range r.Columns {
		status := "ok"
		switch {
		case len(c.Failures) > 0:
			status = "FAIL"
		case c.Unregistered:
			status = "ok (unregistered, valid JSON only)"
		}
		fmt.Fprintf(w, "  %-*s  %9s rows   %s\n", width, c.Key, thousands(c.Rows), status)
	}
	for _, c := range r.Columns {
		for _, f := range c.Failures {
			fmt.Fprintf(w, "  %s id=%s: %s\n", c.Key, f.ID, f.Reason)
		}
	}
	fmt.Fprintf(w, "  %d columns, %d rows fail strict decode, %d case-variant keys\n", len(r.Columns), r.DecodeFails, r.VariantKeys)
	if r.FailedRows > 0 {
		fmt.Fprintln(w, "FAILED")
		return
	}
	fmt.Fprintln(w, "OK")
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// runDoctorJSON checks the stored JSON of the database at dsn and prints the
// report. It returns exitError{1} when any row fails and an operationalError
// (exit 2) when it cannot connect or query. The connection is read-only and
// the command only SELECTs.
func runDoctorJSON(ctx context.Context, dsn string, out io.Writer) error {
	if err := doctorJSON(ctx, dsn, out); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			return err
		}
		return operationalError{err}
	}
	return nil
}

// operationalError is a failure of the doctor itself (as opposed to bad rows).
// Main exits with 2 for it, so scripts can tell it from failing rows (1).
type operationalError struct{ err error }

func (e operationalError) Error() string { return e.err.Error() }
func (e operationalError) Unwrap() error { return e.err }

func doctorJSON(ctx context.Context, dsn string, out io.Writer) error {
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only, nothing to commit

	rep, err := checkStoredJSON(ctx, pgJSONSource{tx})
	if err != nil {
		return err
	}
	rep.render(out)
	if rep.FailedRows > 0 {
		return exitError{code: 1}
	}
	return nil
}

// pgJSONSource reads the JSON columns of the public schema.
type pgJSONSource struct{ tx pgx.Tx }

func (s pgJSONSource) Columns(ctx context.Context) ([]jsonColumn, error) {
	rows, err := s.tx.Query(ctx, `
		SELECT c.table_name, c.column_name,
		       EXISTS (SELECT 1 FROM information_schema.columns i
		                WHERE i.table_schema = 'public' AND i.table_name = c.table_name AND i.column_name = 'id')
		  FROM information_schema.columns c
		  JOIN information_schema.tables t
		    ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		 WHERE c.table_schema = 'public' AND c.data_type IN ('json', 'jsonb')
		 ORDER BY c.table_name, c.column_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []jsonColumn
	for rows.Next() {
		var c jsonColumn
		if err := rows.Scan(&c.Table, &c.Column, &c.HasID); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (s pgJSONSource) Rows(ctx context.Context, c jsonColumn, fn func(id string, raw []byte) error) error {
	idExpr := "ctid::text"
	if c.HasID {
		idExpr = "id::text"
	}
	q := fmt.Sprintf(`SELECT %s, %s::text FROM %s WHERE %s IS NOT NULL ORDER BY 1`,
		idExpr, pgx.Identifier{c.Column}.Sanitize(), pgx.Identifier{"public", c.Table}.Sanitize(), pgx.Identifier{c.Column}.Sanitize())
	rows, err := s.tx.Query(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		if err := fn(id, raw); err != nil {
			return err
		}
	}
	return rows.Err()
}
