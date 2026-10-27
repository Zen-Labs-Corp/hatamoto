package pluginhost

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A plugin's own data lives in Postgres schema plugin_<name>, owned by the
// LOGIN role hm_p_<id8>_<name> (id8: the first 8 hex digits of this database's
// id, because roles are cluster-wide). The role has its own random password and its
// own small connection pool, so plugin SQL runs on connections whose *session
// user* is the plugin role: SET ROLE, RESET ROLE, SET SESSION AUTHORIZATION
// and set_config('role', ...) can only ever land on the plugin role itself,
// which is a member of nothing and owns nothing outside its schema. Core code
// never uses a plugin pool and plugin SQL never runs on the core pool, so
// nothing a plugin leaves behind in its session (role, search_path, temp
// tables, advisory locks) can reach a core query.
//
// The SQL guard (sqlguard.go) is defence in depth. It is NOT the boundary.
//
// Creating a role goes through the hatamoto_admin SECURITY DEFINER function
// (deploy/sql/role-admin-bootstrap.sql, so the application role needs no extra
// attribute) or, failing that, needs CREATEROLE/superuser. Without either,
// kv still works but db.* and plugin migrations report ErrRoleUnavailable.
// The plugin role authenticates with a password over the same host and
// database as DATABASE_URL, so pg_hba.conf must allow password auth for it
// (docs/PLUGIN_API.md).

// ErrRoleUnavailable means the plugin's role could not be created or used.
var ErrRoleUnavailable = errors.New("plugin database role unavailable")

// Per-plugin role limits. A role gets a few more connections than its pool
// holds so a replaced pool can drain while the new one connects.
const (
	pluginPoolConns   = 2
	pluginConnLimit   = 4
	pluginRoleTimeout = "10s"
)

// roleSettings are applied with ALTER ROLE ... SET. temp_file_limit is
// superuser-only; it is applied when the provisioning user may set it.
var roleSettings = [][2]string{
	{"statement_timeout", pluginRoleTimeout},
	{"idle_in_transaction_session_timeout", "15s"},
	{"lock_timeout", "2s"},
}

const tempFileLimit = "64MB"

// newPassword returns a random 32-byte password.
func newPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// scramVerifier returns the SCRAM-SHA-256 verifier of password, the form
// Postgres stores. The role statements carry the verifier, never the
// password. (The password itself is stored in hatamoto_plugin_credentials, so a
// server that logs bind parameters, log_statement=mod, would show that INSERT:
// treat the server log as sensitive.)
func scramVerifier(password string) (string, error) {
	const iter = 4096
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, iter, 32)
	if err != nil {
		return "", err
	}
	mac := func(key []byte, msg string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac(salted, "Client Key"))
	e := base64.StdEncoding
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iter, e.EncodeToString(salt),
		e.EncodeToString(stored[:]), e.EncodeToString(mac(salted, "Server Key"))), nil
}

// Provision creates the plugin's role and schema (idempotent) and opens its
// pool lazily. An error means provisioning failed outright; a role that
// cannot be created is not an error here (see RoleError).
func (d *PluginDB) Provision(ctx context.Context, plugin string) error {
	return d.provision(ctx, plugin, false)
}

// Reprovision rotates the plugin's password, replaces its pool and provisions
// it again, so a role that was missing (no CREATEROLE yet) is retried when
// the plugin loads or its grants change.
func (d *PluginDB) Reprovision(ctx context.Context, plugin string) error {
	return d.provision(ctx, plugin, true)
}

func (d *PluginDB) provision(ctx context.Context, plugin string, rotate bool) error {
	old, err := d.swapState(ctx, plugin, rotate)
	if old != nil && old.pool != nil {
		// Closing waits for in-flight queries: never while holding d.mu.
		old.pool.Close()
	}
	return err
}

// swapState provisions plugin and installs the new state, returning the state
// it replaced (whose pool the caller closes).
func (d *PluginDB) swapState(ctx context.Context, plugin string, rotate bool) (*dbState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state[plugin] != nil && !rotate {
		return nil, nil
	}
	old := d.state[plugin]
	delete(d.state, plugin)

	st := &dbState{}
	if err := d.guardPublicObjects(ctx); err != nil {
		st.roleErr = err
		d.state[plugin] = st
		return old, nil
	}
	role, pw, err := d.provisionRole(ctx, plugin, rotate)
	switch {
	case errors.Is(err, ErrRoleUnavailable):
		st.roleErr = err
		d.log.Warn("plugin database role not created; db.* and plugin migrations are off",
			slog.String("plugin", plugin), slog.Any("error", err))
	case err != nil:
		return old, fmt.Errorf("pluginhost: provision %q: %w", plugin, err)
	default:
		st.role, st.password = role, pw
	}
	d.state[plugin] = st
	return old, nil
}

// provisionRole makes the plugin's role and schema usable, trying in order:
// (a) the SECURITY DEFINER function of deploy/sql/role-admin-bootstrap.sql,
// when the hatamoto_admin schema exists; (b) CREATE ROLE directly, when the
// database user has CREATEROLE or is a superuser (docker/dev installs, CI);
// (c) else ErrRoleUnavailable with the instruction for the one-time
// bootstrap. It returns the role's name and password.
func (d *PluginDB) provisionRole(ctx context.Context, plugin string, rotate bool) (string, string, error) {
	if len(plugin) > maxPluginNameLen {
		return "", "", fmt.Errorf("%w: plugin name longer than %d bytes", ErrRoleUnavailable, maxPluginNameLen)
	}
	var password, roleName string
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('hatamoto.plugin.provision'))`); err != nil {
			return err
		}
		var role, schema string
		var viaFunc, direct bool
		if err := tx.QueryRow(ctx, `SELECT
			to_regprocedure('hatamoto_admin.provision_plugin_role(text,text)') IS NOT NULL
				AND has_function_privilege('hatamoto_admin.provision_plugin_role(text,text)', 'EXECUTE'),
			EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND (rolsuper OR rolcreaterole))`).Scan(&viaFunc, &direct); err != nil {
			return err
		}
		if !viaFunc && !direct {
			return fmt.Errorf("%w: run deploy/sql/role-admin-bootstrap.sql once as a superuser (see docs/PLUGIN_API.md)", ErrRoleUnavailable)
		}
		dbID, err := instanceID(ctx, tx)
		if err != nil {
			return err
		}
		roleName = roleNameFor(dbID, plugin)
		role, schema = ident(roleName), ident(SchemaName(plugin))
		if viaFunc {
			// The bootstrap and the core migrations must agree on the id, or the
			// function would manage roles this host does not look for.
			var same bool
			if err := tx.QueryRow(ctx, `SELECT hatamoto_admin.db_id() = $1`, dbID).Scan(&same); err != nil {
				return roleError(err)
			}
			if !same {
				return fmt.Errorf("%w: hatamoto_admin.db_id() differs from hatamoto_instance; re-run deploy/sql/role-admin-bootstrap.sql", ErrRoleUnavailable)
			}
		} else {
			// Direct mode: only a role this database recorded (its credential row)
			// may be reused; a role of that name made elsewhere is not ours.
			var foreign bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)
				AND NOT EXISTS (SELECT 1 FROM hatamoto_plugin_credentials WHERE plugin = $2)`, roleName, plugin).Scan(&foreign); err != nil {
				return err
			}
			if foreign {
				return fmt.Errorf("%w: role %s exists but was not created for this database", ErrRoleUnavailable, roleName)
			}
		}
		if password, err = d.credential(ctx, tx, plugin, rotate); err != nil {
			return err
		}
		verifier, err := scramVerifier(password)
		if err != nil {
			return err
		}
		if viaFunc {
			_, err = tx.Exec(ctx, `SELECT hatamoto_admin.provision_plugin_role($1, $2)`, pluginSuffix(plugin), verifier)
		} else {
			err = createRoleDirect(ctx, tx, roleName, plugin, verifier)
		}
		if err != nil {
			return roleError(err)
		}
		// The role owns its schema. CREATE SCHEMA ... AUTHORIZATION needs the
		// SET option on the role, which provisioning granted to the app role.
		if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema+` AUTHORIZATION `+role); err != nil {
			return roleError(err)
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	// A database that revoked CONNECT from PUBLIC needs this; without it the
	// pool's login fails with a permission error naming the grant.
	if _, err := d.pool.Exec(ctx, `GRANT CONNECT ON DATABASE `+ident(d.dbName())+` TO `+ident(roleName)); err != nil {
		d.log.Warn("plugin role: GRANT CONNECT failed; the role can connect only if PUBLIC may",
			slog.String("plugin", plugin), slog.Any("error", err))
	}
	return roleName, password, nil
}

// instanceID reads this database's id (hatamoto_instance, made by the core
// migrations).
func instanceID(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (string, error) {
	var id string
	if err := q.QueryRow(ctx, `SELECT id FROM hatamoto_instance`).Scan(&id); err != nil {
		return "", fmt.Errorf("database id: %w", err)
	}
	if len(id) < 8 {
		return "", fmt.Errorf("database id %q is too short", id)
	}
	return id, nil
}

// credential returns the plugin's stored password, creating or (rotate)
// replacing it.
func (d *PluginDB) credential(ctx context.Context, tx pgx.Tx, plugin string, rotate bool) (string, error) {
	if !rotate {
		var pw string
		err := tx.QueryRow(ctx, `SELECT password FROM hatamoto_plugin_credentials WHERE plugin = $1`, plugin).Scan(&pw)
		if err == nil {
			return pw, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	pw, err := newPassword()
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO hatamoto_plugin_credentials (plugin, password) VALUES ($1, $2)
		ON CONFLICT (plugin) DO UPDATE SET password = EXCLUDED.password, rotated_at = now()`, plugin, pw)
	return pw, err
}

func (d *PluginDB) dbName() string { return d.pool.Config().ConnConfig.Database }

func createRoleDirect(ctx context.Context, tx pgx.Tx, role, plugin, verifier string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(ctx, `CREATE ROLE `+ident(role)+` NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`); err != nil {
			return err
		}
	} else {
		// ALTER ROLE cannot (re)state these attributes without holding them.
		var odd bool
		if err := tx.QueryRow(ctx, `SELECT rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls
			FROM pg_roles WHERE rolname = $1`, role).Scan(&odd); err != nil {
			return err
		}
		if odd {
			return fmt.Errorf("%w: role %s exists with unexpected attributes", ErrRoleUnavailable, role)
		}
	}
	// format('%L') quotes the verifier; it never appears in this program's SQL text.
	var alter string
	if err := tx.QueryRow(ctx, `SELECT format('ALTER ROLE %I WITH LOGIN NOINHERIT CONNECTION LIMIT %s PASSWORD %L',
		$1::text, $2::int, $3::text)`, role, pluginConnLimit, verifier).Scan(&alter); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, alter); err != nil {
		return err
	}
	for _, kv := range roleSettings {
		if _, err := tx.Exec(ctx, `ALTER ROLE `+ident(role)+` SET `+kv[0]+` = '`+kv[1]+`'`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `ALTER ROLE `+ident(role)+` SET search_path = `+ident(SchemaName(plugin))); err != nil {
		return err
	}
	// temp_file_limit is superuser-only (or needs GRANT SET ON PARAMETER).
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	if _, err := sp.Exec(ctx, `ALTER ROLE `+ident(role)+` SET temp_file_limit = '`+tempFileLimit+`'`); err != nil {
		_ = sp.Rollback(ctx)
	} else if err := sp.Commit(ctx); err != nil {
		return err
	}
	var ver int
	if err := tx.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&ver); err != nil {
		return err
	}
	// The application may SET ROLE to the plugin role (to create its schema)
	// but does not inherit it. Membership runs app -> plugin only; the plugin
	// role is a member of nothing.
	grant := `GRANT ` + ident(role) + ` TO CURRENT_USER`
	if ver >= 160000 {
		grant += ` WITH INHERIT FALSE, SET TRUE`
	}
	_, err = tx.Exec(ctx, grant)
	return err
}

func roleError(err error) error {
	if errors.Is(err, ErrRoleUnavailable) {
		return err
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "42501" {
		return fmt.Errorf("%w: run deploy/sql/role-admin-bootstrap.sql once as a superuser (see docs/PLUGIN_API.md): %s", ErrRoleUnavailable, pe.Message)
	}
	return fmt.Errorf("%w: %w", ErrRoleUnavailable, err)
}

// RoleError reports why db.* is unavailable for plugin (nil when usable).
func (d *PluginDB) RoleError(plugin string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.state[plugin]
	if st == nil {
		return errors.New("plugin database not provisioned")
	}
	return st.roleErr
}

// pluginPool returns the plugin's own pool, connecting as its role. A role
// that cannot log in (pg_hba.conf has no password rule for it, say) is
// ErrRoleUnavailable with the fix in the message, and is retried next call.
func (d *PluginDB) pluginPool(ctx context.Context, plugin string) (*pgxpool.Pool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.state[plugin]
	if st == nil {
		return nil, errors.New("plugin database not provisioned")
	}
	if st.roleErr != nil {
		return nil, st.roleErr
	}
	if st.pool != nil {
		return st.pool, nil
	}
	cfg, err := pgxpool.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cc := d.pool.Config().ConnConfig.Copy()
	cc.User, cc.Password = st.role, st.password
	cc.RuntimeParams = map[string]string{"application_name": "hatamoto-plugin-" + plugin}
	cc.AfterConnect, cc.ValidateConnect = nil, nil
	// No named prepared statements: DISCARD ALL on release would drop them
	// under the driver's feet.
	cc.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	cc.StatementCacheCapacity, cc.DescriptionCacheCapacity = 0, 0
	cfg.ConnConfig = cc
	cfg.MaxConns, cfg.MinConns = pluginPoolConns, 0
	cfg.MaxConnIdleTime = time.Minute
	cfg.MaxConnLifetime = time.Hour
	// A connection goes back to the pool clean: whatever the plugin set in its
	// session (GUCs, temp tables, advisory locks) is gone, or the connection is.
	cfg.AfterRelease = func(c *pgx.Conn) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := c.Exec(ctx, `DISCARD ALL`)
		return err == nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "42501" {
			return nil, fmt.Errorf("%w: %s may not connect to the database: %v (CONNECT is revoked from PUBLIC; run GRANT CONNECT ON DATABASE %s TO %s as the database owner)",
				ErrRoleUnavailable, st.role, err, ident(d.dbName()), ident(st.role))
		}
		return nil, fmt.Errorf("%w: cannot connect as %s: %v (pg_hba.conf must allow password authentication for plugin roles on the host DATABASE_URL uses, e.g. `host all all 127.0.0.1/32 scram-sha-256`; see docs/PLUGIN_API.md)",
			ErrRoleUnavailable, st.role, err)
	}
	st.pool = pool
	return pool, nil
}

// Close closes every plugin pool.
func (d *PluginDB) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, st := range d.state {
		if st.pool != nil {
			st.pool.Close()
			st.pool = nil
		}
	}
}

// Drop removes a plugin's data, schema, role and credentials (tests;
// uninstall comes with v1-18).
func (d *PluginDB) Drop(ctx context.Context, plugin string) error {
	d.mu.Lock()
	st := d.state[plugin]
	delete(d.state, plugin)
	d.mu.Unlock()
	if st != nil && st.pool != nil {
		st.pool.Close()
	}

	dbID, err := instanceID(ctx, d.pool)
	if err != nil {
		return err
	}
	role := roleNameFor(dbID, plugin)
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			return err
		}
		if exists {
			// The role owns the schema; the app holds its SET option, not its privileges.
			if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+ident(role)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DROP SCHEMA IF EXISTS `+ident(SchemaName(plugin))+` CASCADE`); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `RESET ROLE`); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_plugin_kv WHERE plugin = $1`, plugin); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM hatamoto_plugin_credentials WHERE plugin = $1`, plugin)
		return err
	})
	if err != nil {
		return err
	}
	// The CONNECT grant made at provisioning (a dependency of the role).
	_, _ = d.pool.Exec(ctx, `REVOKE CONNECT ON DATABASE `+ident(d.dbName())+` FROM `+ident(role))
	var viaFunc bool
	if err := d.pool.QueryRow(ctx, `SELECT to_regprocedure('hatamoto_admin.drop_plugin_role(text)') IS NOT NULL
		AND has_function_privilege('hatamoto_admin.drop_plugin_role(text)', 'EXECUTE')`).Scan(&viaFunc); err != nil {
		return err
	}
	if viaFunc {
		_, err := d.pool.Exec(ctx, `SELECT hatamoto_admin.drop_plugin_role($1)`, pluginSuffix(plugin))
		return err
	}
	_, err = d.pool.Exec(ctx, `DROP ROLE IF EXISTS `+ident(role))
	return err
}
