---
title: Run on managed PostgreSQL
nav_title: Managed PostgreSQL
description: Run Hatamoto on a managed PostgreSQL cluster where you have no superuser: create the roles and database in the right order, set up plugin roles, size the pools and connect over verified TLS.
next: backups.md, configuration-reference.md, hardening.md
---

In this guide you run a Hatamoto site on a managed PostgreSQL cluster, where the most privileged account you get is an admin role that is not a superuser. At the end the site migrates, plugins that use their own database work, and backups and restores run with that same admin role.

## Before you start

- PostgreSQL 16 or newer, and the connection details of the admin user the provider gives you. It needs the attributes `NOSUPERUSER CREATEROLE CREATEDB`. Hatamoto never needs more.
- The `psql` client, and a checkout or download of `deploy/sql/role-admin-bootstrap.sql` from the Hatamoto repository.
- If the provider restricts extensions, `citext` and `uuid-ossp` must be allowed. Hatamoto's migrations create both, as the owner of the database, and need nothing else beyond ordinary table rights.
- The provider's CA certificate as a file on the Hatamoto host.

Hosts, users and passwords below are examples. Use `~/.pgpass` or `PGPASSWORD` instead of passwords in URLs.

## Steps

1. Create the application role and the database. The order matters: the admin role must be allowed to create objects in the database, and a database owned by the application role needs that right granted. Connect as the admin role (`admin`) to the `postgres` database:

   ```sql
   CREATE ROLE site_app LOGIN PASSWORD 'change-me';
   GRANT site_app TO admin WITH SET TRUE;
   CREATE DATABASE site OWNER site_app;
   ```

   `GRANT ... WITH SET TRUE` lets the admin role create the database on behalf of `site_app`. The membership inherits by default, and that is what gives the admin role `CREATE` on the database, with the right to grant it, from `site_app`, the database owner. Keep the membership inheriting if you rely on that (an admin that does not inherit needs `CREATE ... WITH GRANT OPTION` granted to it directly). The bootstrap reads the database id from the core table as the admin role if it can, else as `site_app`: after the first start only `site_app` can read that table, so rerunning the bootstrap by hand needs `SET` on `site_app` (the `WITH SET TRUE` above), not inheritance. Restoring a backup as the admin role still requires an inheriting membership.

2. Run the bootstrap once, as the admin role, in the new database, naming the application role. This is the same script a superuser runs, and it is safe to rerun:

   ```bash
   psql -X -v ON_ERROR_STOP=1 -v app_role=site_app \
     "postgres://admin@db.example.com:5432/site?sslmode=verify-full&sslrootcert=/etc/ssl/provider-ca.pem" \
     -f deploy/sql/role-admin-bootstrap.sql
   ```

   It creates a role that can create plugin roles and nothing else, and the application role gains no attribute. A managed provider usually does not let the admin role grant `SET` on the `temp_file_limit` parameter. The script then skips that grant and prints a notice, and plugin roles run without a temporary file size cap. This is not an error. The Data card on the plugin's settings page tells you when the cap is not applied, and the server logs one warning, `plugindb: temp_file_limit not applied`.

3. Point Hatamoto at the database as the application role, with verified TLS. `sslmode=verify-full` checks the certificate and the host name, and `sslrootcert` names the CA file:

   ```bash
   DATABASE_URL="postgres://site_app@db.example.com:5432/site?sslmode=verify-full&sslrootcert=/etc/ssl/provider-ca.pem"
   ```

   The setting reaches the main pool, migrations, plugin pools, `backup create` and `backup restore`. With `DATABASE_DIRECT_URL` set, migrations, plugin pools and backups use that URL instead, with whatever TLS options you put in it (nothing is inherited from `DATABASE_URL`, so repeat them).

4. Connect directly, or through PgBouncer (the next step). A pooler in session mode also works. Transaction mode needs `DATABASE_DIRECT_URL`: without it the migration lock and the import lock do not hold and plugin roles cannot log in, so use a direct connection or session mode instead. Other transaction-mode poolers are untested.

5. Optional: put PgBouncer in front of Postgres. Hatamoto is safe behind PgBouncer in transaction pooling mode (`pool_mode = transaction`) when:

   - PgBouncer is 1.21 or newer and sets `max_prepared_statements` above 0 (for example `100`). Hatamoto keeps the default prepared statement mode of its Postgres driver, which needs it.
   - `DATABASE_URL` points at PgBouncer and `DATABASE_DIRECT_URL` points at Postgres itself, with the same database name and user. Without `DATABASE_DIRECT_URL`, everything uses `DATABASE_URL`, which is only safe on a direct connection or a session-mode pooler. Hatamoto refuses to start if the two URLs name a different user or database.
   - PgBouncer lists the application role and its password in its `auth_file`.
   - If PgBouncer rejects connection settings the driver sends, add them to `ignore_startup_parameters` (`extra_float_digits` is the usual one). Ignored parameters, such as `options=-c ...`, are silently dropped for pooled connections.

   Work that needs a real Postgres session uses `DATABASE_DIRECT_URL`: migrations, the import lock, the attribution backfill lock, `backup create` and `backup restore`, the pre-update backup, and plugin databases. Plugin databases stay off the pooler on purpose, because each plugin logs in as its own role and a pooler cannot be told when a plugin password changes. Hatamoto uses no `LISTEN` or `NOTIFY` and no other session features on the pooled connections.

   Count the connections on Postgres itself: PgBouncer's `default_pool_size` for each database and user pair, plus up to 2 direct connections for every loaded plugin that uses `db` (each plugin role is limited to 4), plus 1 for each running import or backfill, plus 1 for a running `pg_dump`, plus 2 while migrations run. Postgres `max_connections` must cover the sum. Plugin roles must be allowed to log in to Postgres directly (see the `pg_hba.conf` rule in [the configuration reference](configuration-reference.md)).

6. Size the pools. The main pool keeps `DATABASE_MIN_CONNECTIONS` (default 5) connections open and grows to `DATABASE_MAX_CONNECTIONS` (default 25). Every plugin that uses `db` adds a pool of up to 2 connections. With many sites on one cluster, add these up per site and compare the total with the cluster's `max_connections`. For small sites, lower both, for example `DATABASE_MIN_CONNECTIONS=1` and `DATABASE_MAX_CONNECTIONS=5`. See the [configuration reference](configuration-reference.md).

7. Plugin roles log in from the Hatamoto host with a password. If the provider controls login rules instead of `pg_hba.conf`, make sure the application database accepts password logins for new roles from that host.

8. Start Hatamoto. It runs its migrations as the application role.

9. To restore a backup, give the admin role to the restore command. Create the empty target database in the same order as in step 1, then:

   ```bash
   hatamoto backup restore 20261025T101500Z-manual \
     --admin-url "postgres://admin@db.example.com:5432/postgres?sslmode=verify-full&sslrootcert=/etc/ssl/provider-ca.pem" \
     --config-out /opt/hatamoto/config/hatamoto.env
   ```

   The command accepts a superuser or a role with `CREATEROLE` and `CREATEDB` that can create objects in the target database, and refuses anything else before it writes.

## Check it worked

Run `hatamoto migrate status`: every migration is applied. Run `curl -s http://127.0.0.1:8090/ready`. In `psql` as the application role, `SELECT rolcreaterole FROM pg_roles WHERE rolname = current_user;` returns `f`. Grant a plugin that uses `db`: it loads without a `data_error`, and its settings page shows the temporary file note only if the provider refused the grant.

If a plugin answers `unavailable` and mentions `role-admin-bootstrap`, step 2 has not run in this database, or ran as a role that lacks `CREATEROLE`, or `CREATE` on the database with the grant option (inherited from `site_app`). Rerun it after fixing the grants.
