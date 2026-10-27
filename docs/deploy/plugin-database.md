# Deploy notes: plugin database roles

Plugins that hold `kv`/`db` get their own `LOGIN` role (`hm_p_<id8>_<name>`, `<id8>` being the first 8 hex digits of the
database's id in the table `hatamoto_instance`: roles are cluster-wide, so several
Hatamoto databases can share one cluster without sharing plugin roles) and
their own connection pool (docs/PLUGIN_API.md, "Own data"). Two things must be
true on the Postgres host:

1. **Role creation.** `scripts/ship.sh` runs `deploy/sql/role-admin-bootstrap.sql`
   as `postgres` in its preflight (idempotent; PostgreSQL 16+). It makes a
   per-database role admin `hatamoto_role_admin_<id8>` and the functions that
   record which plugin roles this database created; they refuse to touch, grant,
   rotate or drop any other role.
2. **Password authentication over TCP for the plugin roles.** The plugin pool
   connects to the host in `DATABASE_URL` as the plugin role, with a password.
   `pg_hba.conf` needs a rule that matches, e.g. on hatamoto.dev:

   ```
   host  all  all  127.0.0.1/32  scram-sha-256
   ```

   (and `DATABASE_URL` must use `127.0.0.1`/`localhost`, not a Unix socket with
   `peer` authentication). Without it `db.*` answers `unavailable` with that
   message; `kv` and the rest of the site are unaffected. Check with:

   ```sh
   sudo -u postgres psql -d hatamoto_core -Atc "select rolname from pg_roles where rolname like 'hm_p_%'"
   ```

3. **Nothing in the database may be open to `PUBLIC` that the application role
   does not own.** Plugin roles are ordinary roles, so a `PUBLIC` grant is a grant to
   them. The core migration closes `PUBLIC` on what the application role owns in
   schema `public`; it cannot revoke on objects owned by someone else (a table
   `postgres` created with `GRANT SELECT ... TO PUBLIC`, a `SECURITY DEFINER`
   function run as `postgres`). At boot and whenever a plugin is provisioned the
   host lists such objects, logs a warning, shows them as `public_objects` in
   `GET /api/v1/plugins`, and answers `db.*` with `unavailable` (naming them) until
   their owner closes them:

   ```sql
   REVOKE ALL ON TABLE public.legacy_secrets FROM PUBLIC;
   REVOKE ALL ON FUNCTION public.su_fn() FROM PUBLIC;
   ```

   Reload the plugin (or re-grant it) afterwards. To accept the exposure anyway set
   `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1`.

Accepted residual: advisory locks are cluster-session state, so a plugin can take
`pg_advisory_lock` keys for up to its call deadline (the SQL guard refuses `pg_*`
names; the timeouts bound the rest).

Release note: public plugin routes live under `/p/<plugin>/...`, so an existing
page or redirect whose path starts with `/p/<a>/<b>` is answered by the plugin
router (JSON 404 when no such plugin route exists) instead of the page.
