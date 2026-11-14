package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hatamoto.dev/core/internal/media"
)

// rollbackLedger records what a restore has done, so a failed run can undo
// exactly that and nothing else.
type rollbackLedger struct {
	coreDone         bool     // the core dump is committed
	schemasBefore    []string // schemas the target database had before the run
	bootstrapStarted bool     // the role bootstrap ran (or began to)
	plugins          []string // plugins whose provisioning was attempted

	store           media.BlobStore // where mediaKeys were written
	mediaKeys       []string        // keys written, including one that failed
	mediaDirCreated bool            // MediaDir did not exist before the run

	secretsCreated    string // path of the secrets.env this run created
	secretsDirCreated bool
	configCreated     string // path of the config this run created
}

// leftover is something a rollback could not undo, with the command that does.
type leftover struct{ What, Cmd string }

// RestoreFailure is a failed restore after its rollback.
type RestoreFailure struct {
	Cause     error
	Rolled    []string   // what was undone
	Leftovers []leftover // what could not be; empty means the target is unchanged
}

func (e *RestoreFailure) Unwrap() error { return e.Cause }

func (e *RestoreFailure) Error() string {
	var b strings.Builder
	b.WriteString(e.Cause.Error())
	switch {
	case len(e.Rolled) > 0:
		b.WriteString("\nrolled back: " + strings.Join(e.Rolled, ", "))
	case len(e.Leftovers) == 0:
		b.WriteString("\nrolled back: nothing had been written")
	}
	if len(e.Leftovers) == 0 {
		b.WriteString("\ntarget unchanged: fix the cause and run the command again")
		return b.String()
	}
	b.WriteString("\nrollback incomplete: the target is NOT unchanged; remove each leftover:")
	for _, l := range e.Leftovers {
		fmt.Fprintf(&b, "\n  %s: %s", l.What, l.Cmd)
	}
	return b.String()
}

const rollbackTimeout = 2 * time.Minute

// rollback undoes what the failed run wrote, newest first: config, secrets,
// media, then the database. Every step is attempted; the ones that fail are
// reported as leftovers. It always returns a *RestoreFailure.
func (x *restorer) rollback(ctx context.Context, cause error) error {
	// A Ctrl-C that stopped the restore must not stop its cleanup.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	f := &RestoreFailure{Cause: cause}
	var roles []string
	var emptied, removedConfig, removedSecrets bool

	if p := x.led.configCreated; p != "" {
		removedConfig = f.removeFile("config "+p, p)
	}
	if p := x.led.secretsCreated; p != "" {
		removedSecrets = f.removeFile("secrets.env "+p, p)
		if removedSecrets && x.led.secretsDirCreated {
			os.Remove(filepath.Dir(p)) // not empty: something else lives there
		}
	}
	mediaRemoved := x.rollbackMedia(ctx, f)
	if x.led.coreDone || x.led.bootstrapStarted {
		emptied, roles = x.rollbackDatabase(ctx, f)
	}

	if emptied {
		f.Rolled = append(f.Rolled, "database emptied")
	}
	if len(roles) > 0 {
		f.Rolled = append(f.Rolled, "roles "+strings.Join(roles, ", ")+" dropped")
	}
	if mediaRemoved > 0 {
		f.Rolled = append(f.Rolled, fmt.Sprintf("%d media objects removed", mediaRemoved))
	}
	if removedSecrets {
		f.Rolled = append(f.Rolled, "secrets.env removed")
	}
	if removedConfig {
		f.Rolled = append(f.Rolled, "config removed")
	}
	return f
}

// removeFile removes path; a failure becomes a leftover.
func (f *RestoreFailure) removeFile(what, path string) bool {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		f.Leftovers = append(f.Leftovers, leftover{what, "rm " + shellQuote(path)})
		return false
	}
	return true
}

// rollbackMedia deletes the keys this run wrote, then the directories it
// created, and returns how many objects it removed.
func (x *restorer) rollbackMedia(ctx context.Context, f *RestoreFailure) int {
	removed := 0
	for _, key := range x.led.mediaKeys {
		err := x.led.store.Delete(ctx, key)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			f.Leftovers = append(f.Leftovers, leftover{"media " + key, "delete " + shellQuote(key) + " from the media store"})
			continue
		}
		removed++
		if x.o.MediaStore == nil {
			removeEmptyParents(filepath.Join(x.o.MediaDir, filepath.FromSlash(key)), x.o.MediaDir)
		}
	}
	if x.o.MediaStore == nil && x.led.mediaDirCreated {
		if err := os.Remove(x.o.MediaDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			f.Leftovers = append(f.Leftovers, leftover{"media dir " + x.o.MediaDir, "rmdir " + shellQuote(x.o.MediaDir)})
		}
	}
	return removed
}

// removeEmptyParents removes the directories above file, up to but not
// including root, as long as they are empty.
func removeEmptyParents(file, root string) {
	for dir := filepath.Dir(file); dir != root && strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

// rollbackDatabase empties the target database and drops the roles the run
// created. It reports whether the database was emptied and which roles went.
func (x *restorer) rollbackDatabase(ctx context.Context, f *RestoreFailure) (emptied bool, roles []string) {
	d := &dbRestorer{restorer: x}
	if err := d.parseTarget(); err != nil {
		f.Leftovers = append(f.Leftovers, leftover{"database", "empty the target database by hand"})
		return false, nil
	}
	appEnv, err := pgEnv("DATABASE_URL", x.o.DatabaseURL, true)
	if err != nil {
		f.Leftovers = append(f.Leftovers, leftover{"database", "empty the target database by hand"})
		return false, nil
	}
	sql := func(env []string, what, stmt string) bool {
		out, err := x.run(ctx, env, "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-c", stmt)
		if err != nil {
			f.Leftovers = append(f.Leftovers, leftover{what, fmt.Sprintf("psql -d %s -c %s", shellQuote(d.dbName), shellQuote(stmt))})
			x.log.Warn("restore rollback step failed", "step", what, "error", restoreError(out, err))
			return false
		}
		return true
	}
	adminEnv := func() []string {
		env, err := pgEnv("--admin-url", x.req.AdminURL, true)
		if err != nil {
			return nil
		}
		return append(withoutVar(env, "PGDATABASE"), "PGDATABASE="+d.dbName)
	}

	// The plugin schemas and roles first: the role drop needs hatamoto_admin.
	for i := len(x.led.plugins) - 1; i >= 0; i-- {
		name := strings.TrimPrefix(schemaName(x.led.plugins[i]), "plugin_")
		schema := schemaName(x.led.plugins[i])
		ok := sql(appEnv, "plugin "+x.led.plugins[i]+" schema", dropOwnedSchemaSQL(schema))
		// Provisioning granted CONNECT on the database; the role cannot be dropped while it holds it.
		if role := x.roleNamed("hm_p_", "_"+name); role != "" {
			ok = sql(appEnv, "plugin "+x.led.plugins[i]+" database privileges", "REVOKE ALL ON DATABASE "+quoteRole(d.dbName)+" FROM "+quoteRole(role)) && ok
		}
		if sql(appEnv, "plugin "+x.led.plugins[i]+" role", "SELECT hatamoto_admin.drop_plugin_role("+sqlLiteral(name)+")") && ok {
			if r := x.roleNamed("hm_p_", "_"+name); r != "" {
				roles = append(roles, r)
			}
		}
	}
	if x.led.bootstrapStarted {
		env := adminEnv()
		admin := x.roleNamed("hatamoto_role_admin_", "")
		ok := sql(env, "schema hatamoto_admin", dropAdminSchemaSQL)
		if admin != "" {
			sql(env, "temp_file_limit grant of "+admin, revokeTempFileLimitSQL(admin))
			if sql(env, "role "+admin, "DROP ROLE IF EXISTS "+quoteRole(admin)) && ok {
				roles = append(roles, admin)
			}
		}
	}
	if x.led.coreDone {
		emptied = sql(appEnv, "schemas of database "+d.dbName, emptyDatabaseSQL(x.led.schemasBefore))
	}
	return emptied, roles
}

// roleNamed finds the role this run recorded with the given prefix and suffix.
func (x *restorer) roleNamed(prefix, suffix string) string {
	for _, r := range x.mf.Roles {
		if strings.HasPrefix(r, prefix) && strings.HasSuffix(r, suffix) {
			return r
		}
	}
	return ""
}

// dropOwnedSchemaSQL drops a schema as the role that owns it (the app role has
// SET on the plugin roles, not their privileges).
func dropOwnedSchemaSQL(schema string) string {
	return `DO $$ DECLARE o name; BEGIN ` +
		`SELECT pg_get_userbyid(nspowner) INTO o FROM pg_namespace WHERE nspname = ` + sqlLiteral(schema) + `; ` +
		`IF o IS NOT NULL THEN EXECUTE format('SET LOCAL ROLE %I', o); EXECUTE format('DROP SCHEMA %I CASCADE', ` + sqlLiteral(schema) + `); END IF; END $$`
}

// dropAdminSchemaSQL drops hatamoto_admin as the admin URL's role, which sets
// the role that owns the schema first unless it is a superuser.
const dropAdminSchemaSQL = `DO $$ DECLARE o name; BEGIN ` +
	`SELECT pg_get_userbyid(nspowner) INTO o FROM pg_namespace WHERE nspname = 'hatamoto_admin'; ` +
	`IF o IS NOT NULL THEN ` +
	`IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN EXECUTE format('SET LOCAL ROLE %I', o); END IF; ` +
	`DROP SCHEMA hatamoto_admin CASCADE; END IF; END $$`

func revokeTempFileLimitSQL(role string) string {
	return `DO $$ BEGIN IF current_setting('server_version_num')::int >= 150000 AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = ` +
		sqlLiteral(role) + `) THEN EXECUTE format('REVOKE SET ON PARAMETER temp_file_limit FROM %I', ` + sqlLiteral(role) + `); END IF; END $$`
}

// emptyDatabaseSQL drops the schemas the dump created (those not in before)
// and recreates public as a new database has it. It runs as the database owner.
func emptyDatabaseSQL(before []string) string {
	keep := "ARRAY['public','information_schema']::name[]"
	if len(before) > 0 {
		lits := make([]string, len(before))
		for i, s := range before {
			lits[i] = sqlLiteral(s)
		}
		keep = "ARRAY['public','information_schema'," + strings.Join(lits, ",") + "]::name[]"
	}
	return `DO $$ DECLARE s name; BEGIN ` +
		`FOR s IN SELECT nspname FROM pg_namespace WHERE nspname <> ALL (` + keep + `) AND nspname NOT LIKE 'pg\_%' ` +
		`LOOP EXECUTE format('DROP SCHEMA %I CASCADE', s); END LOOP; ` +
		`DROP SCHEMA IF EXISTS public CASCADE; ` +
		`IF current_setting('server_version_num')::int >= 150000 THEN ` +
		`CREATE SCHEMA public AUTHORIZATION pg_database_owner; GRANT USAGE ON SCHEMA public TO PUBLIC; ` +
		`ELSE CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO PUBLIC; END IF; END $$`
}

func sqlLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
