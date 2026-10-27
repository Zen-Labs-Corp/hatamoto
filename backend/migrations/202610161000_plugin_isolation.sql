-- +goose Up
-- Plugins get their own LOGIN role and connection pool (docs/PLUGIN_API.md
-- "Own data"). These two tables are core-only: no plugin role has any right
-- on them.
--
-- hatamoto_plugin_credentials holds each plugin role's random login password,
-- so a restart (or a JWT_SECRET rotation) does not break plugins.
CREATE TABLE hatamoto_plugin_credentials (
    plugin     text PRIMARY KEY,
    password   text NOT NULL,
    rotated_at timestamptz NOT NULL DEFAULT now()
);

-- hatamoto_plugin_kv is the plugin key/value store (host functions kv.*).
CREATE TABLE hatamoto_plugin_kv (
    plugin     text NOT NULL,
    key        text NOT NULL,
    value      jsonb NOT NULL,
    expires_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin, key)
);

-- hatamoto_instance is this database's stable identity (one row). Postgres
-- roles are cluster-wide, so plugin roles are named hm_p_<first 8 hex of id>_<plugin>
-- and two Hatamoto databases on one cluster never share a role. A database
-- that already ran deploy/sql/role-admin-bootstrap.sql keeps the id the
-- bootstrap chose.
CREATE TABLE hatamoto_instance (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    id        text NOT NULL CHECK (id ~ '^[0-9a-f]{32}$')
);
-- +goose StatementBegin
DO $$
DECLARE
    v text;
BEGIN
    IF to_regprocedure('hatamoto_admin.db_id()') IS NOT NULL
       AND has_function_privilege('hatamoto_admin.db_id()', 'EXECUTE') THEN
        EXECUTE 'SELECT hatamoto_admin.db_id()' INTO v;
    END IF;
    INSERT INTO hatamoto_instance (id) VALUES (coalesce(v, replace(gen_random_uuid()::text, '-', '')));
END
$$;
-- +goose StatementEnd

-- Nothing in schema public may be reachable through PUBLIC: plugin roles are
-- ordinary roles, so a PUBLIC grant would be a grant to them. Core tables
-- grant nothing to PUBLIC today; this closes tables, sequences and
-- (non-extension) routines that do, and makes future objects start closed.
-- Objects the application role does not own are skipped here (it cannot
-- revoke on them); the plugin host reports them at boot and refuses db.*
-- while they are open (docs/deploy/plugin-database.md).
-- +goose StatementBegin
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT c.oid::regclass AS obj, c.relkind
        FROM pg_class c
        WHERE c.relnamespace = 'public'::regnamespace
          AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
          AND pg_has_role(current_user, c.relowner, 'USAGE')
          AND EXISTS (SELECT 1 FROM aclexplode(c.relacl) a WHERE a.grantee = 0)
          AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
    LOOP
        IF r.relkind = 'S' THEN
            EXECUTE format('REVOKE ALL ON SEQUENCE %s FROM PUBLIC', r.obj);
        ELSE
            EXECUTE format('REVOKE ALL ON TABLE %s FROM PUBLIC', r.obj);
        END IF;
    END LOOP;

    FOR r IN
        SELECT p.oid::regprocedure AS obj
        FROM pg_proc p
        WHERE p.pronamespace = 'public'::regnamespace
          AND p.prokind IN ('f', 'p', 'a', 'w')
          AND pg_has_role(current_user, p.proowner, 'USAGE')
          AND has_function_privilege('public', p.oid, 'EXECUTE')
          AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
    LOOP
        EXECUTE format('REVOKE ALL ON ROUTINE %s FROM PUBLIC', r.obj);
    END LOOP;
END
$$;
-- +goose StatementEnd

ALTER DEFAULT PRIVILEGES REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES REVOKE ALL ON SEQUENCES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

-- +goose Down
DROP TABLE hatamoto_instance;
DROP TABLE hatamoto_plugin_kv;
DROP TABLE hatamoto_plugin_credentials;
