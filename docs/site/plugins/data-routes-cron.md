---
title: Data, routes and cron
nav_title: Data, routes and cron
description: How a plugin stores its own data, answers web requests and runs on a schedule, and how to prepare the database for plugin data.
---

Beyond hooks, a plugin can keep its own data, serve HTTP endpoints and run on a schedule. This page covers all three, plus the Postgres setup that plugin data needs. To add a stored value, a route and a cron job to a plugin step by step, follow [Keep data and add a route](keep-data-and-routes.md).

## Own data: kv and db

A plugin that holds `kv` or `db` gets its own Postgres schema `plugin_<name>` (a `-` in the name becomes `_`) and its own `LOGIN` role named `hm_p_<id8>_<name>`. The `<id8>` is the first 8 hex digits of the database's id, so two Hatamoto databases in one cluster never share a role. The database is the boundary, not the SQL guard.

- **Own role and pool.** The role has a random 32-byte password, stored in a core table no plugin role can read, rotated when the plugin is re-provisioned and handed to Postgres only as a SCRAM-SHA-256 verifier. Each plugin has its own small connection pool (at most 2 connections) that authenticates as the plugin role. The role is a member of no other role, so `SET ROLE` and similar tricks can only land on itself. Plugin SQL never runs on the pool Hatamoto uses for its own queries.
- **Privileges.** The role owns its schema and nothing else. It is `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT` with a connection limit of 4, a 10 second `statement_timeout` and `search_path = plugin_<name>`. It cannot read `hatamoto_users` or any other core table, server files or other sessions' query text.
- **`kv`** is a key-value store, a table touched only by the host: keys of 1 to 256 bytes, values of any JSON up to 256 KiB, at most 10,000 keys and 64 MiB of values per plugin, with optional TTLs (`ttl_seconds`).
- **`db.query` and `db.exec`** run in one transaction on the plugin's pool, beginning with `SET LOCAL search_path = plugin_<name>`. Use unqualified table names. Arguments are `$1`, `$2` and so on, filled from the JSON `args` array.

```go
var out struct {
	Rows [][]any `json:"rows"`
}
err := guest.Call(sdk.FnDBQuery, map[string]any{
	"sql":  "SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]') FROM (SELECT id, email FROM leads ORDER BY id DESC LIMIT $1) t",
	"args": []any{20},
}, &out)
```

Returning one document per query (`jsonb_agg` as above) means the guest never deals with column types.

### The SQL guard

As defence in depth, the host sends exactly one statement per call on the extended protocol, and only if it passes a lexical guard. It must start with `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `WITH` or `VALUES`. It must not use `set_config`, `current_setting`, `session_user`, `copy`, `public`, `information_schema`, `SELECT ... INTO`, any `pg_*` name, `dblink*`, the `*_to_xml` family, `ts_stat`, `ts_rewrite` or the large-object functions. A refused statement returns `blocked` and is never sent.

### Migrations

Put `NNN_name.sql` files in `migrations/` in the package, in goose format (only the `-- +goose Up` section runs):

```sql
-- +goose Up
CREATE TABLE leads (
    id    bigserial PRIMARY KEY,
    email text NOT NULL,
    seen  timestamptz NOT NULL DEFAULT now()
);
```

Migrations run through goose on the plugin's own pool, as the plugin role, with a version table in the plugin's own schema. Each runs once, in version order, when the plugin loads with `db` granted or when `db` is granted. A migration may `CREATE`, `ALTER` or `DROP` tables, indexes, views, sequences and types and use plain DML. Functions, procedures, triggers, extensions, roles, schemas, policies, `GRANT` and temp tables are refused, and the whole directory is checked before anything runs. A failing migration rolls back and is reported as the plugin's `data_error` while the plugin stays loaded. There is no disk quota on a plugin's tables yet.

## Setting up Postgres for plugin data

Plugin pools connect to the host in `DATABASE_URL` as a different user, with a password. If a plugin pool cannot connect, `db.*` and migrations answer `unavailable` with the reason, while `kv` is unaffected.

**Password authentication.** `pg_hba.conf` must allow it, for example:

```
host  all  all  127.0.0.1/32  scram-sha-256
```

A deployment that reaches Postgres over a Unix socket with `peer` authentication only needs a `host` rule for the plugin roles and a `DATABASE_URL` that points at that TCP address.

**Creating roles.** Creating `hm_p_<id8>_<name>` takes `CREATEROLE`, which the application's own role should not hold on a shared cluster. The host tries, in order:

**Step 1: The bootstrap functions.** Run once as the PostgreSQL superuser in the Hatamoto database (PostgreSQL 16 or later; running the script again changes nothing):

```sh
sudo -u postgres psql -X -v ON_ERROR_STOP=1 -v app_role=<application user> \
  -d <hatamoto database> -f deploy/sql/role-admin-bootstrap.sql
```

It creates a per-database role `hatamoto_role_admin_<id8>` and, in the schema `hatamoto_admin`, the `SECURITY DEFINER` functions `provision_plugin_role` and `drop_plugin_role`. They record each role they create and refuse to touch any role not recorded for this database. The application role gains no attribute.

**Step 2: Direct `CREATE ROLE`**, when the application role itself has `CREATEROLE` or is a superuser. This is typical for Docker installs where the app user owns the cluster, and needs nothing extra.

**Step 3.** Otherwise `db.*` and migrations answer `unavailable` and the reason is shown as `data_error`; `kv` and everything else still work.

**Nothing open to PUBLIC.** Plugin roles are ordinary roles, so a grant to `PUBLIC` is a grant to them. A Hatamoto migration closes `PUBLIC` on the objects the application role owns. It cannot revoke on objects owned by someone else. At boot and whenever a plugin is provisioned the host lists such objects, logs a warning, shows them as `public_objects` in `GET /api/v1/plugins` and answers `db.*` with `unavailable` until their owner closes them:

```sql
REVOKE ALL ON TABLE public.legacy_secrets FROM PUBLIC;
```

Reload the plugin or re-grant it afterwards. To accept the exposure anyway, set `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1`. To check which plugin roles exist:

```sh
sudo -u postgres psql -d <hatamoto database> -Atc "select rolname from pg_roles where rolname like 'hm_p_%'"
```

## Routes

Declare routes in `plugin.json` and serve them from the `http_handle` export. Non-public routes live at `/api/v1/p/<plugin>/<path>` behind admin authentication and CSRF, for the roles listed (owner only by default; API keys cannot reach them). Public routes live at `/p/<plugin>/<path>`, anonymous and rate limited. The two mount points are fixed, so a plugin cannot shadow a core route, and the paths `/p/...` are reserved on the public site. A wrong method on a known path is `405` and an unknown path `404`. Routes appear and disappear when a plugin loads, reloads or unloads, with no restart.

The request is JSON on the `http_handle` input and the response is JSON on its output:

```json
{ "method": "POST", "path": "/hooks/42", "query": { "a": ["1"] },
  "headers": { "content-type": "application/json" },
  "body": "{...}", "body_b64": "",
  "user": { "id": "uuid", "role": "owner" } }
```

```json
{ "status": 200, "headers": { "Content-Type": "application/json" }, "body": "{...}", "body_b64": "" }
```

- `user` is absent on public routes. `pattern` is the manifest route that matched and `path_values` holds its wildcard values. A path the router would clean or redirect (`..`, `//`) is `404`, never another route.
- Request headers passed are `Accept`, `Accept-Language`, `Content-Type`, `User-Agent`, `Referer`, `Origin` and `X-Request-Id`. Never `Authorization`, cookies or the client address.
- The request body is at most 1 MiB (`413` beyond) and arrives as `body`, or `body_b64` when it is not UTF-8.
- Response headers passed are `Content-Type`, `Cache-Control`, `Location`, `Content-Disposition` and `ETag`. `status` defaults to 200 and must be 200 to 599. `Location` may only be a same-site path, so a route cannot become an open redirect.
- Every response is `Cache-Control: no-store` unless the plugin sets it, and carries a sandboxing `Content-Security-Policy` and `nosniff`, so returned HTML runs without scripts in an opaque origin.
- A trap, timeout or error is a `502` (`503` when saturated or switched off) with `{"error":"plugin error"}`.

In Go:

```go
guest.OnHTTP(func(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var kv struct{ Found bool `json:"found"` }
	if err := guest.Call(sdk.FnKVGet, map[string]string{"key": "hits"}, &kv); err != nil {
		return sdk.HTTPResponse{Status: 503, Body: err.Error()}, nil
	}
	return sdk.HTTPResponse{Body: "ok"}, nil
})

//go:wasmexport http_handle
func httpHandle() int32 { return guest.Run(guest.HTTPExport) }
```

## Cron

`"cron": [ { "name": "digest", "every": "15m" } ]` calls the export `on_cron_digest` on the in-process scheduler, the one that runs Hatamoto's own maintenance. `every` is a Go duration of at least one minute. The first call is one period after the plugin loads or the server starts, and a run that is still going when the next is due delays it instead of overlapping. The input is `{ "name": "digest", "scheduled_at": "<RFC 3339>" }` and the output is ignored. A failure is logged. Jobs are added and removed with the plugin, with no restart. They do not run when the process runs with the worker off (`HATAMOTO_WORKER=off`) or in drain mode (`WORKER_DRAIN`).

```go
guest.OnCron("digest", func(ev sdk.CronEvent) error { return nil })

//go:wasmexport on_cron_digest
func onCronDigest() int32 { return guest.Run(guest.CronExport("digest")) }
```

Cron is the right place for slow work. Action hooks should only record what happened, and a cron job drains the queue within a time budget.
