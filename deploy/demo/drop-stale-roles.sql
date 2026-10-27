-- Drops the roles an earlier template left in the demo's own cluster: the role admin
-- (hatamoto_role_admin_<id>) and the plugin roles (hm_p_<id>_<name>) of each instance id. Every
-- template rebuild (an upgrade) runs the bootstrap on an empty database, which gives a new instance
-- id and so a new set; the old ones would pile up. Run it as the cluster's superuser AFTER the old
-- demo and template databases are dropped (a role that still owns objects cannot be dropped):
--
--   sudo -u hatamoto-demo-pg psql -X -h /run/hatamoto-demo-pg -p 5442 -U postgres -v ON_ERROR_STOP=1 -f drop-stale-roles.sql
--
-- It only ever matches those two name patterns, in this cluster; the demo's own logins are not touched.
\set ON_ERROR_STOP on
SELECT current_setting('cluster_name') = 'hatamoto-demo' AS is_demo_cluster \gset
\if :is_demo_cluster
\else
  \echo 'drop-stale-roles.sql: this is not the hatamoto-demo cluster; nothing was done'
  \quit
\endif
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT rolname FROM pg_roles WHERE rolname LIKE 'hm\_p\_%' OR rolname LIKE 'hatamoto\_role\_admin\_%' ORDER BY (rolname LIKE 'hm\_p\_%') DESC LOOP
    EXECUTE format('DROP OWNED BY %I', r.rolname);  -- its grants on server parameters and shared objects
    EXECUTE format('DROP ROLE %I', r.rolname);
    RAISE NOTICE 'dropped role %', r.rolname;
  END LOOP;
END $$;
