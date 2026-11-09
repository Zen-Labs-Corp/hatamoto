-- One-time, idempotent bootstrap that lets the application create the Postgres
-- roles of plugins (docs/PLUGIN_API.md "Own data") WITHOUT giving the
-- application's own role CREATEROLE. Run it as a superuser in the Hatamoto
-- database, naming the application's database role. On a managed PostgreSQL
-- without a superuser, a role with CREATEROLE and CREATEDB that also holds CREATE
-- on the database works too (docs/site/operate/managed-postgresql.md has the
-- provisioning order):
--
--   sudo -u postgres psql -X -v ON_ERROR_STOP=1 -v app_role=<application user> \
--     -d <hatamoto database> -f deploy/sql/role-admin-bootstrap.sql
--
-- (scripts/ship.sh does exactly this in its preflight.) Needs PostgreSQL 16+.
--
-- Postgres roles are cluster-wide, so everything here is per DATABASE: several
-- Hatamoto databases (staging, previews, other sites) can share a cluster.
-- This database has a stable id (hatamoto_admin.instance, shared with the core
-- table hatamoto_instance), <id8> below is its first 8 hex digits.
--
-- What it creates:
--   * role hatamoto_role_admin_<id8>: NOLOGIN, CREATEROLE and nothing else (plus
--     SET on the temp_file_limit parameter). PG16 CREATEROLE manages only the
--     roles it created, so it cannot touch any other role in the cluster, in
--     particular not another database's plugin roles. Nobody logs in as it.
--   * schema hatamoto_admin (no access for PUBLIC, USAGE for the app role) with
--     the ownership table plugin_roles (role -> database id) and SECURITY
--     DEFINER functions provision_plugin_role(name, verifier) and
--     drop_plugin_role(name). provision_plugin_role makes the LOGIN role
--     hm_p_<id8>_<name> (random password supplied as a SCRAM verifier,
--     NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS, CONNECTION
--     LIMIT 4, statement/lock/idle timeouts, search_path plugin_<name>). The
--     plugin role is a member of nothing. The functions accept only names
--     matching ^[a-z][a-z0-9_]{0,40}$, derive the role name themselves, and
--     refuse (and never grant, rotate or drop) a role not recorded in
--     plugin_roles for this database. EXECUTE is granted to the application's
--     role and nobody else.
-- A non-superuser (CREATEROLE and CREATEDB, no other privilege) takes a longer
-- path with the same result: it grants itself SET on the new role admin, moves
-- the objects over, and finishes the script as that role. Without the privilege
-- to grant SET ON PARAMETER the temp_file_limit grant is skipped with a NOTICE,
-- and plugin roles then run without that cap.
-- Nothing in the cluster outside the hatamoto_admin schema is altered, and the
-- application's role gains no attribute.

\set ON_ERROR_STOP on

-- A role that owns the database can create objects in public: never resolve a
-- name through it.
SET search_path = pg_catalog, pg_temp;

SELECT :'app_role' ~ '^[A-Za-z_][A-Za-z0-9_$-]{0,62}$' AS app_role_ok,
       EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'app_role') AS app_role_exists,
       current_setting('server_version_num')::int >= 160000 AS pg16
\gset
\if :app_role_ok
\else
  DO $$ BEGIN RAISE EXCEPTION 'role-admin-bootstrap: app_role is not a valid role name'; END $$;
\endif
\if :app_role_exists
\else
  DO $$ BEGIN RAISE EXCEPTION 'role-admin-bootstrap: app_role does not exist'; END $$;
\endif
\if :pg16
\else
  DO $$ BEGIN RAISE EXCEPTION 'role-admin-bootstrap: PostgreSQL 16 or newer is required'; END $$;
\endif

SELECT rolsuper AS is_super FROM pg_roles WHERE rolname = current_user
\gset
\if :is_super
\else
  SELECT has_database_privilege(current_user, current_database(), 'CREATE WITH GRANT OPTION') AS db_create
  \gset
  \if :db_create
  \else
    DO $$ BEGIN RAISE EXCEPTION 'role-admin-bootstrap: this role needs CREATE on the database with the grant option: it inherits both from the database owner role (GRANT <owner> TO <this role> WITH SET TRUE), or the owner grants them (GRANT CREATE ON DATABASE <db> TO <this role> WITH GRANT OPTION)'; END $$;
  \endif
\endif

BEGIN;

\if :is_super
\else
  -- A rerun: the schema is already owned by the role admin, which this role holds
  -- SET on from the first run; work as that role from the start.
  SELECT coalesce((SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'hatamoto_admin'), '') AS prev_owner,
         coalesce((SELECT nspowner <> (SELECT oid FROM pg_roles WHERE rolname = current_user)
                     AND pg_has_role(current_user, nspowner, 'SET')
                     FROM pg_namespace WHERE nspname = 'hatamoto_admin'), false) AS rerun
  \gset
  -- The core id is read as this role (it inherits the app role, which owns the
  -- table); the role admin used on a rerun cannot read it.
  SELECT to_regclass('public.hatamoto_instance') IS NOT NULL AS has_core
  \gset
  \set core_id ''
  \if :has_core
    SELECT id AS core_id FROM public.hatamoto_instance
    \gset
  \endif
  SELECT set_config('hatamoto.core_id', :'core_id', true) AS core_cfg
  \gset
  \if :rerun
    -- CREATE SCHEMA IF NOT EXISTS below still checks the privilege.
    GRANT CREATE ON DATABASE :"DBNAME" TO :"prev_owner";
    SET LOCAL ROLE :"prev_owner";
  \endif
\endif

-- The schema starts out owned by the superuser: the id is chosen before the
-- role admin (whose name contains it) exists.
CREATE SCHEMA IF NOT EXISTS hatamoto_admin;
CREATE TABLE IF NOT EXISTS hatamoto_admin.instance (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  id        text NOT NULL CHECK (id ~ '^[0-9a-f]{32}$')
);
-- A database that already ran the core migrations keeps the id they chose.
DO $$
DECLARE
  core text;
  mine text;
BEGIN
  core := nullif(current_setting('hatamoto.core_id', true), '');
  IF core IS NULL AND to_regclass('public.hatamoto_instance') IS NOT NULL THEN
    EXECUTE 'SELECT id FROM public.hatamoto_instance' INTO core;
  END IF;
  SELECT id INTO mine FROM hatamoto_admin.instance;
  IF mine IS NULL THEN
    INSERT INTO hatamoto_admin.instance (id) VALUES (coalesce(core, replace(gen_random_uuid()::text, '-', '')));
  ELSIF core IS NOT NULL AND core <> mine THEN
    RAISE EXCEPTION 'role-admin-bootstrap: hatamoto_admin.instance and public.hatamoto_instance disagree on the database id';
  END IF;
END
$$;
SELECT id AS db_id, 'hatamoto_role_admin_' || left(id, 8) AS admin_role FROM hatamoto_admin.instance
\gset

SELECT NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'admin_role') AS need_role
\gset
\if :need_role
  CREATE ROLE :"admin_role" NOLOGIN CREATEROLE NOSUPERUSER NOCREATEDB NOREPLICATION NOBYPASSRLS NOINHERIT;
\endif

\if :is_super
ALTER SCHEMA hatamoto_admin OWNER TO :"admin_role";
ALTER TABLE hatamoto_admin.instance OWNER TO :"admin_role";

-- Which plugin roles this database created. The functions touch only these.
CREATE TABLE IF NOT EXISTS hatamoto_admin.plugin_roles (
  role   text PRIMARY KEY,
  db_id  text NOT NULL,
  plugin text NOT NULL
);
ALTER TABLE hatamoto_admin.plugin_roles OWNER TO :"admin_role";
\else
-- Not a superuser: CREATE ROLE gave this role ADMIN on the new role but not SET,
-- and owning an object needs SET. Grant it (skipped when it already holds it),
-- then hand the objects over; the new owner needs CREATE on the schema and, for
-- the schema itself, on the database. The rest of the script runs as that owner
-- (a rerun already is that owner and skips the handover).
\if :rerun
\else
SELECT pg_has_role(current_user, :'admin_role', 'USAGE') AS has_set
\gset
\if :has_set
\else
  GRANT :"admin_role" TO CURRENT_USER WITH SET TRUE, INHERIT FALSE;
\endif
CREATE TABLE IF NOT EXISTS hatamoto_admin.plugin_roles (
  role   text PRIMARY KEY,
  db_id  text NOT NULL,
  plugin text NOT NULL
);
GRANT CREATE ON SCHEMA hatamoto_admin TO :"admin_role";
ALTER TABLE hatamoto_admin.instance OWNER TO :"admin_role";
ALTER TABLE hatamoto_admin.plugin_roles OWNER TO :"admin_role";
GRANT CREATE ON DATABASE :"DBNAME" TO :"admin_role";
ALTER SCHEMA hatamoto_admin OWNER TO :"admin_role";
SET LOCAL ROLE :"admin_role";
\endif
\endif
REVOKE ALL ON hatamoto_admin.instance, hatamoto_admin.plugin_roles FROM PUBLIC;

REVOKE ALL ON SCHEMA hatamoto_admin FROM PUBLIC;
GRANT USAGE ON SCHEMA hatamoto_admin TO :"app_role";

-- The application role's name and the database id are baked in, so a plugin
-- role is only ever granted to that role and named for this database.
SELECT format($q$
  CREATE OR REPLACE FUNCTION hatamoto_admin.app_role() RETURNS text
  LANGUAGE sql IMMUTABLE AS $b$ SELECT %L::text $b$
$q$, :'app_role') AS ddl
\gexec
SELECT format($q$
  CREATE OR REPLACE FUNCTION hatamoto_admin.db_id() RETURNS text
  LANGUAGE sql IMMUTABLE AS $b$ SELECT %L::text $b$
$q$, :'db_id') AS ddl
\gexec

-- Creates or updates the LOGIN role hm_p_<id8>_<name>. password_verifier is the
-- SCRAM-SHA-256 verifier of the role's password (computed by the application,
-- so the password itself never travels as a bind parameter of a role statement).
CREATE OR REPLACE FUNCTION hatamoto_admin.provision_plugin_role(plugin_name text, password_verifier text)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $fn$
DECLARE
  id  text := hatamoto_admin.db_id();
  r   text := 'hm_p_' || left(hatamoto_admin.db_id(), 8) || '_' || plugin_name;
  app text := hatamoto_admin.app_role();
BEGIN
  IF plugin_name IS NULL OR plugin_name !~ '^[a-z][a-z0-9_]{0,40}$' THEN
    RAISE EXCEPTION 'invalid plugin name' USING ERRCODE = '22023';
  END IF;
  IF password_verifier IS NULL OR password_verifier !~ '^SCRAM-SHA-256\$[0-9]+:[A-Za-z0-9+/=]+\$[A-Za-z0-9+/=]+:[A-Za-z0-9+/=]+$' THEN
    RAISE EXCEPTION 'password_verifier must be a SCRAM-SHA-256 verifier' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = r) THEN
    EXECUTE format('CREATE ROLE %I NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', r);
    INSERT INTO hatamoto_admin.plugin_roles (role, db_id, plugin) VALUES (r, id, plugin_name)
      ON CONFLICT (role) DO NOTHING;
  END IF;
  -- Never touch a role this database did not create (another database's role
  -- whose name collided, or one somebody made by hand).
  IF NOT EXISTS (SELECT 1 FROM hatamoto_admin.plugin_roles WHERE role = r AND db_id = id) THEN
    RAISE EXCEPTION 'role % is not managed for this database', r USING ERRCODE = '42501';
  END IF;
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = r
             AND (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls)) THEN
    RAISE EXCEPTION 'role % exists with unexpected attributes', r USING ERRCODE = '42501';
  END IF;
  EXECUTE format('ALTER ROLE %I WITH LOGIN NOINHERIT CONNECTION LIMIT 4 PASSWORD %L', r, password_verifier);
  EXECUTE format('ALTER ROLE %I SET statement_timeout = %L', r, '10s');
  EXECUTE format('ALTER ROLE %I SET idle_in_transaction_session_timeout = %L', r, '15s');
  EXECUTE format('ALTER ROLE %I SET lock_timeout = %L', r, '2s');
  EXECUTE format('ALTER ROLE %I SET search_path = %I', r, 'plugin_' || plugin_name);
  BEGIN
    EXECUTE format('ALTER ROLE %I SET temp_file_limit = %L', r, '64MB');
  EXCEPTION WHEN insufficient_privilege THEN
    NULL; -- needs GRANT SET ON PARAMETER temp_file_limit (the bootstrap grants it)
  END;
  -- The app may SET ROLE to it (to create the plugin's schema) but does not
  -- inherit it. Membership runs app -> plugin only: the plugin role is a
  -- member of nothing.
  EXECUTE format('GRANT %I TO %I WITH INHERIT FALSE, SET TRUE', r, app);
END
$fn$;

CREATE OR REPLACE FUNCTION hatamoto_admin.drop_plugin_role(plugin_name text)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $fn$
DECLARE
  r text := 'hm_p_' || left(hatamoto_admin.db_id(), 8) || '_' || plugin_name;
BEGIN
  IF plugin_name IS NULL OR plugin_name !~ '^[a-z][a-z0-9_]{0,40}$' THEN
    RAISE EXCEPTION 'invalid plugin name' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM hatamoto_admin.plugin_roles WHERE role = r AND db_id = hatamoto_admin.db_id()) THEN
    RETURN; -- not ours: leave it alone
  END IF;
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = r AND NOT rolsuper AND NOT rolcreaterole) THEN
    EXECUTE format('DROP ROLE %I', r);
  END IF;
  DELETE FROM hatamoto_admin.plugin_roles WHERE role = r;
END
$fn$;

ALTER FUNCTION hatamoto_admin.app_role() OWNER TO :"admin_role";
ALTER FUNCTION hatamoto_admin.db_id() OWNER TO :"admin_role";
ALTER FUNCTION hatamoto_admin.provision_plugin_role(text, text) OWNER TO :"admin_role";
ALTER FUNCTION hatamoto_admin.drop_plugin_role(text) OWNER TO :"admin_role";

REVOKE EXECUTE ON FUNCTION hatamoto_admin.app_role(), hatamoto_admin.db_id(),
  hatamoto_admin.provision_plugin_role(text, text), hatamoto_admin.drop_plugin_role(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION hatamoto_admin.db_id(), hatamoto_admin.provision_plugin_role(text, text),
  hatamoto_admin.drop_plugin_role(text) TO :"app_role";

\if :is_super
-- temp_file_limit is superuser-only; let the function set it per plugin role.
GRANT SET ON PARAMETER temp_file_limit TO :"admin_role";
\else
RESET ROLE;
-- The role admin needed CREATE on the database only to own the schema.
REVOKE CREATE ON DATABASE :"DBNAME" FROM :"admin_role";
-- Only a superuser may grant it; without it the plugin roles have no temp cap.
SELECT format($q$
  DO $d$ BEGIN
    GRANT SET ON PARAMETER temp_file_limit TO %I;
  EXCEPTION WHEN insufficient_privilege THEN
    RAISE NOTICE 'role-admin-bootstrap: temp_file_limit cannot be granted by this role; plugin roles run without it';
  END $d$
$q$, :'admin_role') AS ddl
\gexec
\endif

COMMIT;
