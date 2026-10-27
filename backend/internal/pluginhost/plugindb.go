package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Limits of one db.* call.
const (
	maxDBRows        = 1000
	maxDBResultBytes = 1 << 20
	maxDBArgs        = 64
	maxDBSQLBytes    = 64 << 10
	dbLockTimeout    = 2 * time.Second
)

// SchemaName is the Postgres schema of a plugin's data. Plugin names are
// [a-z][a-z0-9-]*, so "-" becomes "_" (no collision: "_" is not a legal name
// character).
func SchemaName(plugin string) string { return "plugin_" + strings.ReplaceAll(plugin, "-", "_") }

// pluginSuffix is the plugin's part of its role name.
func pluginSuffix(plugin string) string { return strings.ReplaceAll(plugin, "-", "_") }

// maxPluginNameLen keeps hm_p_<8 hex>_<name> within Postgres's 63-byte
// identifier limit with room to spare (5+8+1+41 = 55).
const maxPluginNameLen = 41

// roleNameFor is the LOGIN role plugin SQL runs as. Postgres roles are
// cluster-wide, so the role carries the first 8 hex digits of this database's
// id: two Hatamoto databases in one cluster never share a plugin role.
func roleNameFor(dbID, plugin string) string {
	return "hm_p_" + dbID[:8] + "_" + pluginSuffix(plugin)
}

func ident(s string) string { return pgx.Identifier{s}.Sanitize() }

// PluginDB provisions and queries the per-plugin databases (see
// plugindb_provision.go for the model). pool is the core pool: it runs only
// core-authored SQL (provisioning, credentials, kv). Plugin SQL runs only on
// the plugin's own pool.
type PluginDB struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	// guard vets plugin SQL before it is sent. Defence in depth, not the boundary.
	guard func(sql string, mode sqlMode) (string, error)

	// AllowPublicObjects lets db.* be granted although objects the application
	// role does not own are open to PUBLIC (HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1).
	AllowPublicObjects bool

	mu    sync.Mutex
	state map[string]*dbState
}

type dbState struct {
	role     string // the plugin's Postgres role
	roleErr  error  // nil: the role exists and is usable
	password string // the role's login password; never logged
	pool     *pgxpool.Pool
}

// NewPluginDB returns a PluginDB over the core pool.
func NewPluginDB(pool *pgxpool.Pool, log *slog.Logger) *PluginDB {
	if log == nil {
		log = slog.Default()
	}
	return &PluginDB{pool: pool, log: log, guard: guardSQL, state: map[string]*dbState{}}
}

// begin opens a transaction on the plugin's own pool, pinned to its schema,
// with timeouts. The role-level settings already say the same; the local ones
// follow the call's deadline.
func (d *PluginDB) begin(ctx context.Context, plugin string) (pgx.Tx, error) {
	timeout := 5 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		timeout = max(time.Until(dl), 50*time.Millisecond)
	}
	pool, err := d.pluginPool(ctx, plugin)
	if err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	for _, q := range []string{
		`SET LOCAL search_path = ` + ident(SchemaName(plugin)),
		fmt.Sprintf(`SET LOCAL statement_timeout = '%dms'`, timeout.Milliseconds()),
		fmt.Sprintf(`SET LOCAL lock_timeout = '%dms'`, dbLockTimeout.Milliseconds()),
		fmt.Sprintf(`SET LOCAL idle_in_transaction_session_timeout = '%dms'`, timeout.Milliseconds()+1000),
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			_ = tx.Rollback(context.Background())
			return nil, err
		}
	}
	return tx, nil
}

// QueryResult is the result of db.query.
type QueryResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated,omitempty"`
}

// Query runs one guarded statement as the plugin and returns its rows.
func (d *PluginDB) Query(ctx context.Context, plugin, sql string, args ...any) (*QueryResult, error) {
	var res *QueryResult
	err := d.run(ctx, plugin, sql, args, func(rows pgx.Rows) error {
		res = &QueryResult{Rows: [][]any{}}
		for _, f := range rows.FieldDescriptions() {
			res.Columns = append(res.Columns, f.Name)
		}
		size := 0
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				return err
			}
			row := make([]any, len(vals))
			for i, v := range vals {
				row[i] = jsonValue(v)
			}
			b, _ := json.Marshal(row)
			if size += len(b); len(res.Rows) >= maxDBRows || size > maxDBResultBytes {
				res.Truncated = true
				return nil
			}
			res.Rows = append(res.Rows, row)
		}
		return rows.Err()
	}, nil)
	return res, err
}

// Exec runs one guarded statement as the plugin and returns the rows it touched.
func (d *PluginDB) Exec(ctx context.Context, plugin, sql string, args ...any) (int64, error) {
	var n int64
	err := d.run(ctx, plugin, sql, args, func(rows pgx.Rows) error {
		for rows.Next() {
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return nil
	}, &n)
	return n, err
}

func (d *PluginDB) run(ctx context.Context, plugin, sql string, args []any, read func(pgx.Rows) error, affected *int64) error {
	if err := d.RoleError(plugin); err != nil {
		return err
	}
	if len(sql) > maxDBSQLBytes || len(args) > maxDBArgs {
		return fmt.Errorf("%w: statement or argument list too large", ErrSQLBlocked)
	}
	stmt, err := d.guard(sql, sqlDML)
	if err != nil {
		return err
	}
	tx, err := d.begin(ctx, plugin)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck // after commit this is a no-op

	// Extended protocol only: Postgres itself refuses a second statement.
	rows, err := tx.Query(ctx, stmt, append([]any{pgx.QueryExecModeDescribeExec}, args...)...)
	if err != nil {
		return err
	}
	if err := read(rows); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if affected != nil {
		*affected = rows.CommandTag().RowsAffected()
	}
	return tx.Commit(ctx)
}

// jsonValue turns a pgx value into something encoding/json renders usefully.
func jsonValue(v any) any {
	switch x := v.(type) {
	case [16]byte: // uuid
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	case []byte:
		return x // base64 in JSON, as bytea should be
	case fmt.Stringer:
		if _, isTime := v.(time.Time); !isTime {
			if _, isJSON := v.(json.Marshaler); !isJSON {
				return x.String()
			}
		}
	}
	return v
}

// convertArg maps a decoded JSON value to a query argument.
func convertArg(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		f, err := x.Float64()
		return f, err
	case map[string]any, []any:
		b, err := json.Marshal(x)
		return string(b), err
	}
	return v, nil
}

// DecodeArgs decodes a JSON array of query arguments.
func DecodeArgs(raw json.RawMessage) ([]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var in []any
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("args must be a JSON array: %w", err)
	}
	if len(in) > maxDBArgs {
		return nil, fmt.Errorf("more than %d args", maxDBArgs)
	}
	out := make([]any, len(in))
	for i, v := range in {
		a, err := convertArg(v)
		if err != nil {
			return nil, err
		}
		out[i] = a
	}
	return out, nil
}
