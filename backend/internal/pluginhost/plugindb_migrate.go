package pluginhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Migration is one plugin migration file: <version>_<name>.sql in the goose
// format. Only the "-- +goose Up" section runs; Down sections are ignored.
type Migration struct {
	Version int64
	Name    string
	Stmts   []string
}

var migrationFile = regexp.MustCompile(`^(\d{1,18})_[A-Za-z0-9_.-]*\.sql$`)

// ReadMigrations reads dir/*.sql from fsys, validates every statement with the
// SQL guard, and returns them in version order.
func ReadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Migration
	seen := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := migrationFile.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %s: name must be <version>_<name>.sql", e.Name())
		}
		ver, _ := strconv.ParseInt(m[1], 10, 64)
		if ver < 1 {
			return nil, fmt.Errorf("migration %s: version must be positive", e.Name())
		}
		if prev, dup := seen[ver]; dup {
			return nil, fmt.Errorf("migrations %s and %s share version %d", prev, e.Name(), ver)
		}
		seen[ver] = e.Name()
		f, err := fsys.Open(path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
		f.Close()
		if err != nil || len(body) > 1<<20 {
			return nil, fmt.Errorf("migration %s: unreadable or over 1 MiB", e.Name())
		}
		stmts, err := splitSQLStatements(gooseUp(string(body)))
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		for _, s := range stmts {
			if _, err := guardSQL(s, sqlMigration); err != nil {
				return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
			}
		}
		out = append(out, Migration{Version: ver, Name: e.Name(), Stmts: stmts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// gooseUp returns the Up section of a goose migration (the whole file when it
// has no annotations).
func gooseUp(src string) string {
	var up strings.Builder
	in := true
	for _, line := range strings.Split(src, "\n") {
		switch t := strings.ToLower(strings.TrimSpace(line)); {
		case strings.HasPrefix(t, "-- +goose up"):
			in = true
			continue
		case strings.HasPrefix(t, "-- +goose down"):
			in = false
			continue
		case strings.HasPrefix(t, "-- +goose statementbegin"), strings.HasPrefix(t, "-- +goose statementend"):
			continue
		}
		if in {
			up.WriteString(line + "\n")
		}
	}
	return up.String()
}

// Migrate applies the pending migrations through goose on the plugin's own
// pool, so they run as the plugin role with its session user, with a version
// table inside the plugin's own schema (plugin_<name>.goose_db_version). The
// SQL guard vets every statement first; the database is the boundary.
func (d *PluginDB) Migrate(ctx context.Context, plugin string, migs []Migration) (int, error) {
	if len(migs) == 0 {
		return 0, nil
	}
	pool, err := d.pluginPool(ctx, plugin)
	if err != nil {
		return 0, err
	}
	var gm []*goose.Migration
	for _, m := range migs {
		stmts := m.Stmts
		gm = append(gm, goose.NewGoMigration(m.Version, &goose.GoFunc{
			RunTx: func(ctx context.Context, tx *sql.Tx) error { return d.migrateTx(ctx, tx, plugin, stmts) },
		}, nil))
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return 0, err
	}
	prov, err := goose.NewProvider(goose.DialectPostgres, db, nil,
		goose.WithGoMigrations(gm...), goose.WithDisableGlobalRegistry(true),
		goose.WithTableName(SchemaName(plugin)+".goose_db_version"),
		goose.WithSessionLocker(locker), goose.WithAllowOutofOrder(true))
	if err != nil {
		return 0, fmt.Errorf("pluginhost: migrations for %q: %w", plugin, err)
	}
	res, err := prov.Up(ctx)
	if err != nil {
		return len(res), fmt.Errorf("pluginhost: migrate %q: %w", plugin, err)
	}
	return len(res), nil
}

func (d *PluginDB) migrateTx(ctx context.Context, tx *sql.Tx, plugin string, stmts []string) error {
	for _, q := range []string{
		`SET LOCAL search_path = ` + ident(SchemaName(plugin)),
		`SET LOCAL statement_timeout = '60000ms'`,
		`SET LOCAL lock_timeout = '5000ms'`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, s := range stmts {
		stmt, err := d.guard(s, sqlMigration)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
