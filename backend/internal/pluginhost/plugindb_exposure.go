package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Plugin roles are ordinary Postgres roles, so anything PUBLIC may use is
// usable by plugin SQL. The core migrations close PUBLIC on what the
// application role owns in schema public, but cannot revoke on objects owned
// by someone else (a table a superuser created, a SECURITY DEFINER function
// run as postgres). Those stay open to every plugin role, so the host finds
// them, says so loudly, and refuses to hand out db.* while they exist (unless
// the operator accepts the risk with HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1).
// The owner closes them with REVOKE ... FROM PUBLIC (docs/deploy/plugin-database.md).

// publicObjectsSQL lists objects that PUBLIC can use, that live in a schema
// PUBLIC can use, that the application role does not own, and that no
// extension owns. System schemas are skipped.
const publicObjectsSQL = `
SELECT obj FROM (
  SELECT n.nspname || '.' || c.relname || ' (' ||
         CASE c.relkind WHEN 'S' THEN 'sequence' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' ELSE 'table' END ||
         ', owner ' || pg_get_userbyid(c.relowner) || ')' AS obj
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
    AND NOT pg_has_role(current_user, c.relowner, 'USAGE')
    AND CASE WHEN c.relkind = 'S' THEN has_sequence_privilege('public', c.oid, 'USAGE,SELECT,UPDATE')
             ELSE has_table_privilege('public', c.oid, 'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') END
    AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
    AND has_schema_privilege('public', n.oid, 'USAGE')
    AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
  UNION ALL
  SELECT n.nspname || '.' || p.proname || '() (SECURITY DEFINER function, owner ' || pg_get_userbyid(p.proowner) || ')'
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
  WHERE p.prosecdef
    AND NOT pg_has_role(current_user, p.proowner, 'USAGE')
    AND has_function_privilege('public', p.oid, 'EXECUTE')
    AND n.nspname NOT LIKE 'pg\_%' AND n.nspname <> 'information_schema'
    AND has_schema_privilege('public', n.oid, 'USAGE')
    AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
) x ORDER BY obj LIMIT 50`

// PublicObjects lists what PUBLIC can use that the application role cannot
// close (see above). Empty is the healthy answer.
func (d *PluginDB) PublicObjects(ctx context.Context) ([]string, error) {
	rows, err := d.pool.Query(ctx, publicObjectsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// WarnPublicObjects logs the objects PublicObjects finds (boot-time check) and
// returns them.
func (d *PluginDB) WarnPublicObjects(ctx context.Context) []string {
	objs, err := d.PublicObjects(ctx)
	if err != nil {
		d.log.Warn("plugins: could not check for objects open to PUBLIC", slog.Any("error", err))
		return nil
	}
	if len(objs) > 0 {
		d.log.Warn("plugins: objects the application role does not own are open to PUBLIC, so every plugin role can use them; "+exposureAdvice(d.AllowPublicObjects),
			slog.String("objects", strings.Join(objs, "; ")))
	}
	return objs
}

func exposureAdvice(allowed bool) string {
	if allowed {
		return "db.* stays on because HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1; have their owner REVOKE ... FROM PUBLIC"
	}
	return "db.* is off until their owner runs REVOKE ... FROM PUBLIC on them (or HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1 accepts the risk)"
}

// guardPublicObjects returns an ErrRoleUnavailable error naming the open
// objects when db.* must be refused, nil when it may proceed.
func (d *PluginDB) guardPublicObjects(ctx context.Context) error {
	objs, err := d.PublicObjects(ctx)
	if err != nil {
		return fmt.Errorf("%w: cannot check for objects open to PUBLIC: %w", ErrRoleUnavailable, err)
	}
	if len(objs) > 0 {
		d.log.Warn("plugins: objects the application role does not own are open to PUBLIC; "+exposureAdvice(d.AllowPublicObjects),
			slog.String("objects", strings.Join(objs, "; ")))
	}
	if len(objs) == 0 || d.AllowPublicObjects {
		return nil
	}
	return fmt.Errorf("%w: %d object(s) are open to PUBLIC and not owned by the application role (%s); %s",
		ErrRoleUnavailable, len(objs), strings.Join(objs, "; "), exposureAdvice(false))
}
