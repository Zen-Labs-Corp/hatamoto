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

   `GRANT ... WITH SET TRUE` lets the admin role create the database on behalf of `site_app`. Then connect to the new database as `site_app` and give the admin role the right to create objects there:

   ```sql
   GRANT CREATE ON DATABASE site TO admin;
   ```

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

   The setting reaches the main pool, migrations, plugin pools, `backup create` and `backup restore`.

4. Connect directly, or through a pooler in session mode. Never use transaction mode: migrations hold a session level advisory lock for the whole run, and transaction pooling can release it on another connection or never. Plugin migrations do the same.

5. Size the pools. The main pool keeps `DATABASE_MIN_CONNECTIONS` (default 5) connections open and grows to `DATABASE_MAX_CONNECTIONS` (default 25). Every plugin that uses `db` adds a pool of up to 2 connections. With many sites on one cluster, add these up per site and compare the total with the cluster's `max_connections`. For small sites, lower both, for example `DATABASE_MIN_CONNECTIONS=1` and `DATABASE_MAX_CONNECTIONS=5`. See the [configuration reference](configuration-reference.md).

6. Plugin roles log in from the Hatamoto host with a password. If the provider controls login rules instead of `pg_hba.conf`, make sure the application database accepts password logins for new roles from that host.

7. Start Hatamoto. It runs its migrations as the application role.

8. To restore a backup, give the admin role to the restore command. Create the empty target database in the same order as in step 1, then:

   ```bash
   hatamoto backup restore 20261025T101500Z-manual \
     --admin-url "postgres://admin@db.example.com:5432/postgres?sslmode=verify-full&sslrootcert=/etc/ssl/provider-ca.pem" \
     --config-out /opt/hatamoto/config/hatamoto.env
   ```

   The command accepts a superuser or a role with `CREATEROLE` and `CREATEDB` that can create objects in the target database, and refuses anything else before it writes.

## Check it worked

Run `hatamoto migrate status`: every migration is applied. Run `curl -s http://127.0.0.1:8090/ready`. In `psql` as the application role, `SELECT rolcreaterole FROM pg_roles WHERE rolname = current_user;` returns `f`. Grant a plugin that uses `db`: it loads without a `data_error`, and its settings page shows the temporary file note only if the provider refused the grant.

If a plugin answers `unavailable` and mentions `role-admin-bootstrap`, step 2 has not run in this database, or ran as a role that lacks `CREATEROLE` or `CREATE` on the database. Rerun it after fixing the grants.
