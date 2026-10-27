package backup

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/pluginhost"
)

// dbSteps restores the database side of a backup, in the order that works:
// core dump (app role), superuser bootstrap, then each plugin (provision its
// role and schema as the app role, restore its dump as that role). A failure
// names the step; nothing is rolled back.
func (r *restorer) dbSteps(ctx context.Context) error {
	return (&dbRestorer{restorer: r}).steps(ctx)
}

// dbRestorer is the database side of a restore: the restorer plus the target
// read from DatabaseURL.
type dbRestorer struct {
	*restorer
	appRole, dbName string // set by parseTarget

	// provision and ownerOf replace the pluginhost calls in tests.
	provision func(ctx context.Context, plugin string) error
	ownerOf   func(ctx context.Context, schema string) (string, error)
}

func (x *dbRestorer) steps(ctx context.Context) error {
	if err := x.parseTarget(); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "hatamoto-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := x.extractDumps(work); err != nil {
		return fmt.Errorf("extract dumps: %w", err)
	}
	if err := x.restoreCore(ctx, filepath.Join(work, coreName)); err != nil {
		return fmt.Errorf("restore stopped at core database: %w", err)
	}
	if err := x.runBootstrap(ctx, work); err != nil {
		return fmt.Errorf("restore stopped at role bootstrap: %w", err)
	}
	return x.restorePlugins(ctx, work)
}

// parseTarget reads the app role and the database name from DatabaseURL. The
// restore tools get them as arguments; the password only ever goes by env.
func (x *dbRestorer) parseTarget() error {
	// pgx gives the query keys user and dbname precedence over the userinfo
	// and the path; the checks connect through pgx, so read it the same way.
	cfg, err := pgconn.ParseConfig(x.o.DatabaseURL)
	u, uerr := url.Parse(x.o.DatabaseURL)
	if uerr != nil {
		return errors.New("DATABASE_URL needs the app role as its user")
	}
	q := u.Query()
	if err != nil || (u.User.Username() == "" && !q.Has("user")) || cfg.User == "" {
		return errors.New("DATABASE_URL needs the app role as its user")
	}
	x.appRole = cfg.User
	x.dbName = cfg.Database
	// pgx falls back to $PGDATABASE; the URL itself must name the database.
	if x.dbName == "" || (strings.TrimPrefix(u.Path, "/") == "" && q.Get("dbname") == "") {
		return errors.New("DATABASE_URL names no database")
	}
	if !rolePattern.MatchString(x.appRole) {
		return fmt.Errorf("app role %q has an unusual name; restore supports lowercase letters, digits and underscores", x.appRole)
	}
	return nil
}

// extractDumps writes the core dump and the plugin dumps to dir (0600).
func (x *dbRestorer) extractDumps(dir string) error {
	want := map[string]bool{coreName: true}
	for _, name := range x.mf.Plugins {
		want[schemaName(name)+".dump"] = true
	}
	f, err := os.Open(x.src) // #nosec G304 -- operator supplied path
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !want[h.Name] {
			continue
		}
		out, err := os.OpenFile(filepath.Join(dir, h.Name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
}

// restoreCore loads the core dump into the empty database as the app role.
func (x *dbRestorer) restoreCore(ctx context.Context, dump string) error {
	env, err := pgEnv("DATABASE_URL", x.o.DatabaseURL, true)
	if err != nil {
		return err
	}
	out, err := x.run(ctx, env, "pg_restore", "--no-owner", "--role="+x.appRole, "--exit-on-error", "--dbname="+x.dbName, dump)
	if err != nil {
		return errors.New(restoreError(out, err))
	}
	return nil
}

// runBootstrap runs the embedded role-admin bootstrap as the superuser of
// AdminURL against the target database, after a guard that refuses objects the
// dump planted. It needs psql: the script uses psql
// meta-commands.
func (x *dbRestorer) runBootstrap(ctx context.Context, work string) error {
	if x.req.AdminURL == "" {
		return errors.New("no admin URL")
	}
	env, err := pgEnv("--admin-url", x.req.AdminURL, true)
	if err != nil {
		return err
	}
	env = append(withoutVar(env, "PGDATABASE"), "PGDATABASE="+x.dbName)
	file, err := writeTemp0600(work, "role-admin-bootstrap-*.sql", []byte(bootstrapSQL))
	if err != nil {
		return err
	}
	defer os.Remove(file)
	guard, err := writeTemp0600(work, "role-admin-guard-*.sql", []byte(bootstrapGuardSQL))
	if err != nil {
		return err
	}
	defer os.Remove(guard)
	// One session: the guard runs first and ON_ERROR_STOP ends psql on its error.
	out, err := x.run(ctx, env, "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-v", "app_role="+x.appRole, "-f", guard, "-f", file)
	if err != nil {
		return errors.New(restoreError(out, err))
	}
	return nil
}

func withoutVar(env []string, name string) []string {
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, name+"=") {
			out = append(out, e)
		}
	}
	return out
}

// restorePlugins provisions each plugin's role and schema, then loads its dump
// as that role. Provisioning creates the schema, so the dump's own CREATE
// SCHEMA (which the plugin role may not run) is left out of the restore list.
func (x *dbRestorer) restorePlugins(ctx context.Context, work string) error {
	if len(x.mf.Plugins) == 0 {
		return nil
	}
	provision, ownerOf, closeFn, err := x.pluginAPI(ctx)
	if err != nil {
		return fmt.Errorf("restore stopped at plugins: %w", err)
	}
	defer closeFn()
	env, err := pgEnv("DATABASE_URL", x.o.DatabaseURL, true)
	if err != nil {
		return err
	}
	for _, name := range x.mf.Plugins {
		step := "plugin " + name
		schema := schemaName(name)
		if err := provision(ctx, name); err != nil {
			return fmt.Errorf("restore stopped at %s: provision: %w", step, err)
		}
		role, err := ownerOf(ctx, schema)
		if err != nil {
			return fmt.Errorf("restore stopped at %s: %w", step, err)
		}
		if !rolePattern.MatchString(role) {
			return fmt.Errorf("restore stopped at %s: schema %s has no usable owner role (%q)", step, schema, role)
		}
		dump := filepath.Join(work, schema+".dump")
		listing, err := x.run(ctx, env, "pg_restore", "-l", dump)
		if err != nil {
			return fmt.Errorf("restore stopped at %s: list: %s", step, restoreError(listing, err))
		}
		list, err := writeTemp0600(work, schema+"-*.list", []byte(filterSchemaLine(string(listing))))
		if err != nil {
			return err
		}
		out, err := x.run(ctx, env, "pg_restore", "--clean", "--if-exists", "--no-owner", "--role="+role,
			"-L", list, "--dbname="+x.dbName, dump)
		os.Remove(list)
		if err != nil {
			return fmt.Errorf("restore stopped at %s: %s", step, restoreError(out, err))
		}
	}
	return nil
}

// pluginAPI returns the provisioning and owner lookup, from the test hooks or
// from a pool opened on the restored database as the app role.
func (x *dbRestorer) pluginAPI(ctx context.Context) (provision func(context.Context, string) error, ownerOf func(context.Context, string) (string, error), closeFn func(), err error) {
	if x.provision != nil && x.ownerOf != nil {
		return x.provision, x.ownerOf, func() {}, nil
	}
	open := x.o.OpenPool
	if open == nil {
		open = func(ctx context.Context, url string) (*pgxpool.Pool, error) { return pgxpool.New(ctx, url) }
	}
	pool, err := open(ctx, x.o.DatabaseURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot reach the restored database: %w", err)
	}
	pdb := pluginhost.NewPluginDB(pool, x.log)
	ownerOf = func(ctx context.Context, schema string) (string, error) {
		var role string
		err := pool.QueryRow(ctx, `SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = $1`, schema).Scan(&role)
		if err != nil {
			return "", fmt.Errorf("owner of schema %s: %w", schema, err)
		}
		return role, nil
	}
	provision = func(ctx context.Context, plugin string) error {
		return provisionChecked(ctx, plugin, pdb.Provision, pdb.RoleError)
	}
	return provision, ownerOf, func() { pdb.Close(); pool.Close() }, nil
}

// provisionChecked provisions a plugin and then returns why its role could not
// be created, if so: Provision itself reports success for that case, and the
// restore would otherwise go on to fail with a vague "no usable owner".
func provisionChecked(ctx context.Context, plugin string, provision func(context.Context, string) error, roleError func(string) error) error {
	if err := provision(ctx, plugin); err != nil {
		return err
	}
	return roleError(plugin)
}

// filterSchemaLine drops the plugin schema's own CREATE SCHEMA entry from a
// pg_restore -l listing.
func filterSchemaLine(listing string) string {
	var keep []string
	for _, l := range strings.Split(listing, "\n") {
		if strings.Contains(l, " SCHEMA - plugin_") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// restoreError is the first "<tool>: error:" line of a tool's output (the
// later lines repeat the failing command), else the last line.
func restoreError(out []byte, err error) string {
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, ": error:") || strings.HasPrefix(l, "ERROR:") || strings.Contains(l, ": ERROR:") {
			return capLine(l)
		}
	}
	return errorText(out, err)
}
