package packages

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Source says where an installed package came from.
type Source string

// The sources of a package row.
const (
	SourceRegistry Source = "registry"
	SourceUpload   Source = "upload"
	SourceURL      Source = "url"
	SourceDev      Source = "dev"
)

// Row is one installed package as stored, without its archive bytes.
type Row struct {
	Kind        Kind
	Name        string
	Version     string
	Manifest    json.RawMessage
	SHA256      string
	SignerKeyID string // "" for an unsigned package
	Source      Source
	Enabled     bool
	// State is StateInstalled, or StateUninstalling for a removal that has
	// started and can be retried (the package is not loaded).
	State       string
	Size        int
	InstalledAt time.Time
	UpdatedAt   time.Time
}

// Signed reports whether the package carried a signature when it was installed.
func (r Row) Signed() bool { return r.SignerKeyID != "" }

// Package row states.
const (
	StateInstalled    = "installed"
	StateUninstalling = "uninstalling"
)

// Config is the owner's package policy and the active theme.
type Config struct {
	// AllowUnsigned lets unsigned packages be installed (the owner's explicit,
	// warned opt-in).
	AllowUnsigned bool
	// ActiveTheme is the installed theme in use; "" is the built-in default.
	ActiveTheme string
	// ServerEnvironment is the ENVIRONMENT of the server that last booted on
	// this database ("" before the first boot). The CLI reads it so that
	// "production" is the server's decision, not whatever the shell exports.
	ServerEnvironment string
}

// ErrNotFound is returned for a package that is not installed.
var ErrNotFound = errors.New("package not installed")

// Store keeps packages in hatamoto_packages.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns a store over pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// execer is a pool or a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Put installs or replaces (update) the package of the same kind and name. An
// update keeps the stored enabled flag; a new package is stored with enabled.
func (s *Store) Put(ctx context.Context, p *Package, src Source, enabled bool) error {
	return s.put(ctx, s.pool, p, src, enabled)
}

func (s *Store) put(ctx context.Context, db execer, p *Package, src Source, enabled bool) error {
	sum := p.SHA256()
	var sig []byte
	var signer *string
	if p.Signature != nil {
		sig, _ = base64.StdEncoding.DecodeString(p.Signature.Signature)
		signer = &p.Signature.Signer
	}
	_, err := db.Exec(ctx, `
		INSERT INTO hatamoto_packages (kind, name, version, manifest, data, sha256, signature, signer_key_id, source, enabled)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (kind, name) DO UPDATE SET
		    version = EXCLUDED.version, manifest = EXCLUDED.manifest, data = EXCLUDED.data,
		    sha256 = EXCLUDED.sha256, signature = EXCLUDED.signature, signer_key_id = EXCLUDED.signer_key_id,
		    source = EXCLUDED.source, updated_at = now()`,
		string(p.Kind), p.Name, p.Version, []byte(p.Manifest), p.Data(), hex.EncodeToString(sum[:]), sig, signer, string(src), enabled)
	if err != nil {
		return fmt.Errorf("packages: store %s %q: %w", p.Kind, p.Name, err)
	}
	return nil
}

const rowColumns = `kind, name, version, manifest, sha256, COALESCE(signer_key_id, ''), source, enabled, state, octet_length(data), installed_at, updated_at`

func scanRow(rows pgx.Row) (Row, error) {
	var r Row
	var kind, src string
	var manifest []byte
	err := rows.Scan(&kind, &r.Name, &r.Version, &manifest, &r.SHA256, &r.SignerKeyID, &src, &r.Enabled, &r.State, &r.Size, &r.InstalledAt, &r.UpdatedAt)
	r.Kind, r.Source, r.Manifest = Kind(kind), Source(src), manifest
	return r, err
}

// List returns every installed package, plugins first, by name.
func (s *Store) List(ctx context.Context) ([]Row, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+rowColumns+` FROM hatamoto_packages ORDER BY kind DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("packages: list: %w", err)
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one package row.
func (s *Store) Get(ctx context.Context, kind Kind, name string) (Row, error) {
	r, err := scanRow(s.pool.QueryRow(ctx, `SELECT `+rowColumns+` FROM hatamoto_packages WHERE kind = $1 AND name = $2`, string(kind), name))
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	return r, err
}

// Load reads the archive of an installed package and parses it again (the
// bytes are trusted: they were verified on the way in, but parsing keeps one
// code path).
func (s *Store) Load(ctx context.Context, kind Kind, name string) (*Package, Row, error) {
	var data []byte
	row, err := s.Get(ctx, kind, name)
	if err != nil {
		return nil, row, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT data FROM hatamoto_packages WHERE kind = $1 AND name = $2`, string(kind), name).Scan(&data); err != nil {
		return nil, row, err
	}
	p, err := Parse(data, DefaultLimits)
	return p, row, err
}

// SetEnabled flips a plugin's enabled flag.
func (s *Store) SetEnabled(ctx context.Context, kind Kind, name string, on bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE hatamoto_packages SET enabled = $3, updated_at = now() WHERE kind = $1 AND name = $2`, string(kind), name, on)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the package row (and a theme's settings). It touches nothing else.
func (s *Store) Delete(ctx context.Context, kind Kind, name string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM hatamoto_packages WHERE kind = $1 AND name = $2`, string(kind), name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if kind == KindTheme {
			if _, err := tx.Exec(ctx, `UPDATE hatamoto_package_config SET active_theme = NULL, updated_at = now() WHERE active_theme = $1`, name); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `DELETE FROM hatamoto_theme_settings WHERE theme = $1`, name)
			return err
		}
		return nil
	})
}

// Config reads the package policy.
func (s *Store) Config(ctx context.Context) (Config, error) {
	var c Config
	var active *string
	var env *string
	err := s.pool.QueryRow(ctx, `SELECT allow_unsigned, active_theme, server_environment FROM hatamoto_package_config WHERE singleton`).Scan(&c.AllowUnsigned, &active, &env)
	if active != nil {
		c.ActiveTheme = *active
	}
	if env != nil {
		c.ServerEnvironment = *env
	}
	return c, err
}

// SetServerEnvironment records the booting server's ENVIRONMENT.
func (s *Store) SetServerEnvironment(ctx context.Context, env string) error {
	_, err := s.pool.Exec(ctx, `UPDATE hatamoto_package_config SET server_environment = $1 WHERE singleton`, env)
	return err
}

// SetState marks a package row installed or uninstalling.
func (s *Store) SetState(ctx context.Context, kind Kind, name, state string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE hatamoto_packages SET state = $3, updated_at = now() WHERE kind = $1 AND name = $2`, string(kind), name, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ThemeSettingsStamp is when a theme's settings last changed (zero: never). It
// is part of the reconcile key, so every replica picks a change up.
func (s *Store) ThemeSettingsStamp(ctx context.Context, theme string) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `SELECT updated_at FROM hatamoto_theme_settings WHERE theme = $1`, theme).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return t, err
}

// SetAllowUnsigned stores the unsigned-package opt-in.
func (s *Store) SetAllowUnsigned(ctx context.Context, on bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE hatamoto_package_config SET allow_unsigned = $1, updated_at = now() WHERE singleton`, on)
	return err
}

// SetActiveTheme stores the active theme ("" selects the built-in default).
func (s *Store) SetActiveTheme(ctx context.Context, name string) error {
	var v *string
	if name != "" {
		v = &name
	}
	_, err := s.pool.Exec(ctx, `UPDATE hatamoto_package_config SET active_theme = $1, updated_at = now() WHERE singleton`, v)
	return err
}

// ThemeSettings returns a theme's stored settings object ({} when none).
func (s *Store) ThemeSettings(ctx context.Context, theme string) (json.RawMessage, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT settings FROM hatamoto_theme_settings WHERE theme = $1`, theme).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return json.RawMessage(`{}`), nil
	}
	return raw, err
}

// PutThemeSettings stores a theme's settings object.
func (s *Store) PutThemeSettings(ctx context.Context, theme string, v json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO hatamoto_theme_settings (theme, settings) VALUES ($1, $2::jsonb)
		ON CONFLICT (theme) DO UPDATE SET settings = EXCLUDED.settings, updated_at = now()`, theme, []byte(v))
	return err
}
