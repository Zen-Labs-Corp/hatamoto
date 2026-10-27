-- One-time, idempotent setup of the demo's database side. The owner runs it as the superuser of the
-- demo's OWN cluster (docs/deploy/public-demo.md, step 3), through its unix socket: never against the
-- main site's cluster. It touches only the roles and databases named below:
--
--   DEMO_APP_PASSWORD=... DEMO_RESET_PASSWORD=... sudo -E -u hatamoto-demo-pg \
--     psql -X -h /run/hatamoto-demo-pg -p 5442 -U postgres -f deploy/demo/setup.sql
--
-- Run again later it leaves the passwords alone (no env needed); DEMO_ROTATE_PASSWORDS=1 sets new ones.
--
-- Creates
--   * role hatamoto_demo        LOGIN, no superuser, no CREATEDB, no CREATEROLE: the demo service's login.
--                               It owns the demo databases, so the app can migrate and seed them.
--   * role hatamoto_demo_reset  LOGIN, CREATEDB only, member of hatamoto_demo: what reset.sh uses to
--                               drop and recreate hatamoto_demo from the template. Not a superuser,
--                               cannot create roles, has no right on any other database.
--   * database hatamoto_demo_template, owned by hatamoto_demo, commented 'hatamoto-demo-template'.
-- The live database hatamoto_demo is created by the first reset.sh run.
\set ON_ERROR_STOP on
\getenv app_pw DEMO_APP_PASSWORD
\getenv reset_pw DEMO_RESET_PASSWORD
\getenv rotate DEMO_ROTATE_PASSWORDS
-- an unset environment variable leaves the psql variable undefined: make it empty
\if :{?app_pw}
\else
  \set app_pw ''
\endif
\if :{?reset_pw}
\else
  \set reset_pw ''
\endif
\if :{?rotate}
\else
  \set rotate ''
\endif
-- Passwords are set when a role is created, or when DEMO_ROTATE_PASSWORDS=1. A re-run (after
-- a release, to recreate the template) leaves existing passwords alone, so the env files stay valid.
SELECT NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'hatamoto_demo') AS need_app,
       NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'hatamoto_demo_reset') AS need_reset,
       :'rotate' = '1' AS rotate \gset
SELECT (:'need_app'::boolean OR :'need_reset'::boolean OR :'rotate'::boolean) AS need_pw,
       length(:'app_pw') >= 16 AND length(:'reset_pw') >= 16 AS pw_ok \gset
\if :need_pw
  \if :pw_ok
  \else
    \echo 'setup.sql: set DEMO_APP_PASSWORD and DEMO_RESET_PASSWORD in the environment (16+ characters each)'
    \quit
  \endif
\endif

\if :need_app
  CREATE ROLE hatamoto_demo LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 12;
\endif
ALTER ROLE hatamoto_demo WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
\if :need_pw
  ALTER ROLE hatamoto_demo PASSWORD :'app_pw';
\endif
ALTER ROLE hatamoto_demo SET statement_timeout = '30s';
ALTER ROLE hatamoto_demo SET idle_in_transaction_session_timeout = '30s';
-- Disk bounds are cluster-wide, in postgresql.demo.conf (temp_file_limit, max_connections,
-- max_wal_size): this cluster's data dir is on the size-capped filesystem, which is the hard bound.

\if :need_reset
  CREATE ROLE hatamoto_demo_reset LOGIN CREATEDB NOSUPERUSER NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 2;
\endif
ALTER ROLE hatamoto_demo_reset WITH LOGIN CREATEDB NOSUPERUSER NOCREATEROLE NOREPLICATION NOBYPASSRLS;
\if :need_pw
  ALTER ROLE hatamoto_demo_reset PASSWORD :'reset_pw';
\endif
GRANT hatamoto_demo TO hatamoto_demo_reset;
-- reset.sh checks which cluster it is talking to (data_directory, port, cluster_name) before it drops anything
GRANT pg_read_all_settings TO hatamoto_demo_reset;

SELECT NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'hatamoto_demo_template') AS need_tpl \gset
\if :need_tpl
  CREATE DATABASE hatamoto_demo_template OWNER hatamoto_demo;
\endif
COMMENT ON DATABASE hatamoto_demo_template IS 'hatamoto-demo-template';
