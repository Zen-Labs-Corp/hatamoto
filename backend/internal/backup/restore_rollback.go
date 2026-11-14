package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/core/internal/media"
)

// rollbackLedger records what a restore has done, so a failed run can undo
// exactly that and nothing else.
type rollbackLedger struct {
	coreDone         bool     // the core dump is committed
	schemasBefore    []string // schemas the target database had before the run
	public           publicState
	bootstrapStarted bool     // the role bootstrap ran (or began to)
	plugins          []string // plugins whose provisioning was attempted

	store           media.BlobStore // where mediaKeys were written
	mediaKeys       []string        // keys written, including one that failed
	mediaDirCreated bool            // MediaDir did not exist before the run

	secretsCreated    string // path of the secrets.env this run created
	secretsDirCreated bool
	configCreated     string // path of the config this run created
}

// publicState is what schema public held before the run: its objects (as
// "c:<oid>" relations, "p:" functions, "t:" types, "e:" extensions) that are
// not members of something else, and its grants as statements.
type publicState struct {
	exists  bool
	objects []string
	acl     []string
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
	// sqlFix runs stmt; when it fails, remedy is the statement that removes
	// the leftover by hand.
	sqlFix := func(env []string, what, stmt, remedy string) bool {
		out, err := x.run(ctx, env, "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-c", stmt)
		if err != nil {
			f.Leftovers = append(f.Leftovers, leftover{what, fmt.Sprintf("psql -d %s -c %s", shellQuote(d.dbName), shellQuote(remedy))})
			x.log.Warn("restore rollback step failed", "step", what, "error", restoreError(out, err))
			return false
		}
		return true
	}
	sql := func(env []string, what, stmt string) bool { return sqlFix(env, what, stmt, stmt) }
	// roleExists says whether the cluster has the role; when it cannot tell, it
	// says yes, so the cleanup is attempted and a failure is reported.
	roleExists := func(env []string, role string) bool {
		out, err := x.run(ctx, env, "psql", "-X", "-q", "-tA", "-c", "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = "+sqlLiteral(role)+")")
		return err != nil || strings.TrimSpace(string(out)) != "f"
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
		ok := true
		if !slices.Contains(x.led.schemasBefore, schema) {
			ok = sql(appEnv, "plugin "+x.led.plugins[i]+" schema", dropOwnedSchemaSQL(schema))
		}
		// A role exists only if this run created it (the cluster check refuses
		// a run when one is already there); provisioning may have failed before.
		role := x.pluginRole(name)
		if role == "" || !roleExists(appEnv, role) {
			continue
		}
		// Provisioning granted CONNECT on the database; the role cannot be dropped while it holds it.
		ok = sql(appEnv, "plugin "+x.led.plugins[i]+" database privileges", "REVOKE ALL ON DATABASE "+quoteRole(d.dbName)+" FROM "+quoteRole(role)) && ok
		// The remedy must not need hatamoto_admin, which this rollback drops next.
		remedy := "DROP OWNED BY " + quoteRole(role) + "; DROP ROLE " + quoteRole(role)
		if sqlFix(appEnv, "plugin "+x.led.plugins[i]+" role", "SELECT hatamoto_admin.drop_plugin_role("+sqlLiteral(name)+")", remedy) && ok {
			roles = append(roles, role)
		}
	}
	if x.led.bootstrapStarted {
		env := adminEnv()
		admin := x.adminRole()
		ok := true
		if !slices.Contains(x.led.schemasBefore, "hatamoto_admin") {
			ok = sql(env, "schema hatamoto_admin", dropAdminSchemaSQL)
		}
		if admin != "" {
			sql(env, "temp_file_limit grant of "+admin, revokeTempFileLimitSQL(admin))
			if sql(env, "role "+admin, "DROP ROLE IF EXISTS "+quoteRole(admin)) && ok {
				roles = append(roles, admin)
			}
		}
	}
	if x.led.coreDone {
		emptied = sql(appEnv, "schemas of database "+d.dbName, emptyDatabaseSQL(x.led.schemasBefore, x.led.public))
	}
	return emptied, roles
}

// adminRole is the admin role the manifest records, or "".
func (x *restorer) adminRole() string {
	for _, r := range x.mf.Roles {
		if strings.HasPrefix(r, adminRolePrefix) {
			return r
		}
	}
	return ""
}

// pluginRole is the exact role of a plugin, built from the admin role's id, or
// "" when the manifest has no such role.
func (x *restorer) pluginRole(name string) string {
	admin := x.adminRole()
	if admin == "" {
		return ""
	}
	want := "hm_p_" + strings.TrimPrefix(admin, adminRolePrefix) + "_" + name
	if slices.Contains(x.mf.Roles, want) {
		return want
	}
	return ""
}

const adminRolePrefix = "hatamoto_role_admin_"

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

// notMember is the condition that a catalog row was made on its own, not as
// part of an extension or another object.
func notMember(class, alias string) string {
	if class == "pg_class" {
		// Tables and views can carry internal dependencies of their own; only
		// sequences, indexes and partitions belong to another relation.
		return "(" + alias + ".relkind IN ('r','p','v','m','f','c') AND NOT " + alias + ".relispartition AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = " + alias + ".oid AND d.deptype = 'e') OR " +
			alias + ".relkind = 'S' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = " + alias + ".oid AND d.deptype IN ('e','i','a')))"
	}
	return "NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = '" + class + "'::regclass AND d.objid = " + alias +
		".oid AND d.deptype IN ('e','i','a'))"
}

// publicSnapshotSQL lists what public holds now, one text per row: "ns" when
// the schema exists, one "kind:oid" per object (see publicState) and one
// "acl:<GRANT statement>" per privilege on the schema.
var publicSnapshotSQL = `SELECT 'ns' FROM pg_namespace WHERE nspname = 'public'
UNION ALL SELECT 'c:' || c.oid FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND ` + notMember("pg_class", "c") + `
UNION ALL SELECT 'p:' || p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public' AND ` + notMember("pg_proc", "p") + `
UNION ALL SELECT 't:' || t.oid FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname = 'public' AND ` + notMember("pg_type", "t") + `
UNION ALL SELECT 'e:' || oid FROM pg_extension
UNION ALL SELECT 'acl:' || format('GRANT %s ON SCHEMA public TO %s%s', a.privilege_type,
  CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE quote_ident(pg_get_userbyid(a.grantee)) END,
  CASE WHEN a.is_grantable THEN ' WITH GRANT OPTION' ELSE '' END)
  FROM pg_namespace n, aclexplode(n.nspacl) a WHERE n.nspname = 'public'`

// parsePublicState reads the rows of publicSnapshotSQL.
func parsePublicState(rows []string) publicState {
	var st publicState
	for _, r := range rows {
		switch {
		case r == "ns":
			st.exists = true
		case strings.HasPrefix(r, "acl:"):
			st.acl = append(st.acl, strings.TrimPrefix(r, "acl:"))
		case len(r) > 2 && r[1] == ':':
			st.objects = append(st.objects, r)
		}
	}
	return st
}

// emptyDatabaseSQL drops the schemas the dump created (those not in before)
// and, in public, only what the run added: the objects, extensions and grants
// public held before stay (or are put back). It runs as the database owner.
func emptyDatabaseSQL(before []string, pub publicState) string {
	keep := "ARRAY['public','information_schema']::name[]"
	if len(before) > 0 {
		lits := make([]string, len(before))
		for i, s := range before {
			lits[i] = sqlLiteral(s)
		}
		keep = "ARRAY['public','information_schema'," + strings.Join(lits, ",") + "]::name[]"
	}
	head := `DECLARE s name; r record; had text[] := ` + textArray(pub.objects) + `; BEGIN ` +
		`FOR s IN SELECT nspname FROM pg_namespace WHERE nspname <> ALL (` + keep + `) AND nspname NOT LIKE 'pg\_%' ` +
		`LOOP EXECUTE format('DROP SCHEMA %I CASCADE', s); END LOOP; ` +
		`FOR r IN SELECT extname FROM pg_extension WHERE ('e:' || oid) <> ALL (had) ` +
		`LOOP EXECUTE format('DROP EXTENSION IF EXISTS %I CASCADE', r.extname); END LOOP; `
	if !pub.exists {
		return doBlock(head + `DROP SCHEMA IF EXISTS public CASCADE; END`)
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString(`FOR r IN SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace ` +
		`WHERE n.nspname = 'public' AND ('c:' || c.oid) <> ALL (had) AND ` + notMember("pg_class", "c") + ` ORDER BY c.oid DESC ` +
		`LOOP EXECUTE format('DROP %s IF EXISTS public.%I CASCADE', CASE r.relkind WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' ` +
		`WHEN 'f' THEN 'FOREIGN TABLE' WHEN 'S' THEN 'SEQUENCE' WHEN 'c' THEN 'TYPE' ELSE 'TABLE' END, r.relname); END LOOP; `)
	b.WriteString(`FOR r IN SELECT p.proname, pg_get_function_identity_arguments(p.oid) AS args FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace ` +
		`WHERE n.nspname = 'public' AND ('p:' || p.oid) <> ALL (had) AND ` + notMember("pg_proc", "p") + ` ` +
		`LOOP EXECUTE format('DROP ROUTINE IF EXISTS public.%I(%s) CASCADE', r.proname, r.args); END LOOP; `)
	b.WriteString(`FOR r IN SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace ` +
		`WHERE n.nspname = 'public' AND t.typrelid = 0 AND t.typcategory <> 'A' AND ('t:' || t.oid) <> ALL (had) AND ` + notMember("pg_type", "t") + ` ` +
		`LOOP EXECUTE format('DROP TYPE IF EXISTS public.%I CASCADE', r.typname); END LOOP; `)
	b.WriteString(`FOR r IN SELECT DISTINCT CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE quote_ident(pg_get_userbyid(a.grantee)) END AS who ` +
		`FROM pg_namespace n, aclexplode(n.nspacl) a WHERE n.nspname = 'public' AND a.grantee <> n.nspowner ` +
		`LOOP EXECUTE 'REVOKE ALL ON SCHEMA public FROM ' || r.who; END LOOP; `)
	for _, g := range pub.acl {
		b.WriteString("EXECUTE " + sqlLiteral(g) + "; ")
	}
	b.WriteString("END")
	return doBlock(b.String())
}

// doBlock wraps a plpgsql body in DO with a dollar-quote tag the body does not
// contain, so a name holding $$ cannot end the block early.
func doBlock(body string) string {
	tag := "$hm_rb$"
	for strings.Contains(body, tag) {
		tag = "$hm_rb_" + tag[1:]
	}
	return "DO " + tag + " " + body + " " + tag
}

// textArray is a SQL text[] literal of items.
func textArray(items []string) string {
	if len(items) == 0 {
		return "ARRAY[]::text[]"
	}
	lits := make([]string, len(items))
	for i, it := range items {
		lits[i] = sqlLiteral(it)
	}
	return "ARRAY[" + strings.Join(lits, ",") + "]::text[]"
}

func sqlLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
