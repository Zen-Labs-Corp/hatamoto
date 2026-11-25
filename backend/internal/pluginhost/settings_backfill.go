package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
)

// SealReport counts what one SealExisting run did (never values).
type SealReport struct{ Sealed, Plugins, Unreadable int }

// SealExisting seals the plain top-level writeOnly values of the given plugins
// (name -> settings_schema). Each plugin is one transaction that locks its row;
// the row is updated only when something changed, and updated_at is left alone
// (this is not an owner edit). Plugins whose schema has no writeOnly property
// are skipped without a query. A nil key fails before anything is touched. A
// plugin that fails does not stop the others; the errors are joined.
func (s *SettingsStore) SealExisting(ctx context.Context, schemas map[string][]byte) (SealReport, error) {
	var rep SealReport
	if len(s.key) == 0 {
		return rep, errNoSettingsKey
	}
	var errs []error
	for _, plugin := range slices.Sorted(maps.Keys(schemas)) {
		schema := schemas[plugin]
		if !hasWriteOnly(schema) {
			continue
		}
		sealed, unreadable, err := s.sealPlugin(ctx, plugin, schema)
		if err != nil {
			errs = append(errs, fmt.Errorf("pluginhost: seal settings for %q: %w", plugin, err))
			continue
		}
		rep.Sealed += sealed
		rep.Unreadable += unreadable
		if sealed > 0 {
			rep.Plugins++
		}
	}
	return rep, errors.Join(errs...)
}

func hasWriteOnly(schema []byte) bool {
	for _, p := range parseProps(schema).Properties {
		if p.WriteOnly {
			return true
		}
	}
	return false
}

func (s *SettingsStore) sealPlugin(ctx context.Context, plugin string, schema []byte) (sealed, unreadable int, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT settings FROM hatamoto_plugin_settings WHERE plugin = $1 FOR UPDATE`, plugin).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var out jsontext.Value
		out, sealed, unreadable, err = sealLegacy(s.key, plugin, schema, raw)
		if err != nil || sealed == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hatamoto_plugin_settings SET settings = $2::jsonb WHERE plugin = $1`, plugin, []byte(out))
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	return sealed, unreadable, nil
}
