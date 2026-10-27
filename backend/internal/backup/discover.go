package backup

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Plugin is one plugin schema to dump and the role that owns it.
type Plugin struct {
	Name   string // display name, e.g. foo-bar
	Schema string // plugin_foo_bar
	Role   string
}

// Part is the archive file name of the plugin dump.
func (p Plugin) Part() string { return p.Schema + ".dump" }

// Querier is the part of a pgx pool discovery needs.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	schemaPattern = regexp.MustCompile(`^plugin_[a-z0-9_]+$`)
	rolePattern   = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// schemaName mirrors pluginhost.SchemaName (checked in a test).
func schemaName(plugin string) string { return "plugin_" + strings.ReplaceAll(plugin, "-", "_") }

// discover lists the plugin schemas from the catalog. The owner role is read
// from the schema itself, so it is exactly the role to SET ROLE to. Display
// names come from the credentials table when it has them.
func discover(ctx context.Context, q Querier) ([]Plugin, error) {
	rows, err := q.Query(ctx, `SELECT n.nspname, pg_get_userbyid(n.nspowner)
		FROM pg_namespace n WHERE n.nspname LIKE 'plugin\_%' ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("list plugin schemas: %w", err)
	}
	defer rows.Close()
	var out []Plugin
	for rows.Next() {
		var p Plugin
		if err := rows.Scan(&p.Schema, &p.Role); err != nil {
			return nil, fmt.Errorf("list plugin schemas: %w", err)
		}
		if !schemaPattern.MatchString(p.Schema) {
			return nil, fmt.Errorf("unexpected plugin schema name %q", p.Schema)
		}
		if !rolePattern.MatchString(p.Role) {
			return nil, fmt.Errorf("schema %s has no usable owner role (%q)", p.Schema, p.Role)
		}
		p.Name = strings.TrimPrefix(p.Schema, "plugin_")
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list plugin schemas: %w", err)
	}
	rows.Close()
	names := credentialNames(ctx, q)
	for i, p := range out {
		if n, ok := names[p.Schema]; ok {
			out[i].Name = n
		}
	}
	return out, nil
}

// credentialNames maps schema to the real plugin name. Best effort: a missing
// table or row leaves the name derived from the schema.
func credentialNames(ctx context.Context, q Querier) map[string]string {
	names := map[string]string{}
	rows, err := q.Query(ctx, `SELECT plugin FROM hatamoto_plugin_credentials`)
	if err != nil {
		return names
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names[schemaName(n)] = n
		}
	}
	return names
}
