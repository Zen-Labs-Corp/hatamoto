package backup

import (
	_ "embed"
	"os"
	"path/filepath"
)

// bootstrapSQL is a byte copy of deploy/sql/role-admin-bootstrap.sql (go:embed
// cannot reach outside the module); TestBootstrapSQLMatchesDeploy keeps it so.
//
//go:embed role-admin-bootstrap.sql
var bootstrapSQL string

// bootstrapGuardSQL runs first in the same psql session as the bootstrap. The
// bootstrap runs as a superuser and reads public.hatamoto_instance, an object
// the restored dump controls, so a dump could plant a view, trigger, rule or
// column type there and run code as the superuser. The guard refuses unless the
// table is the plain one the migrations create, and refuses a dump that brings
// the hatamoto_admin schema (the dump excludes it; the bootstrap creates it).
// Everything is schema qualified in pg_catalog and search_path is pinned.
const bootstrapGuardSQL = `\set ON_ERROR_STOP on
SET search_path = pg_catalog, pg_temp;
DO $guard$
DECLARE
  rel pg_catalog.regclass := pg_catalog.to_regclass('public.hatamoto_instance');
  kind "char";
BEGIN
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = 'hatamoto_admin') THEN
    RAISE EXCEPTION 'restore guard: schema hatamoto_admin already exists in the target database';
  END IF;
  IF rel IS NULL THEN
    RETURN;
  END IF;
  SELECT relkind INTO kind FROM pg_catalog.pg_class WHERE oid = rel;
  IF kind <> 'r' THEN
    RAISE EXCEPTION 'restore guard: public.hatamoto_instance is not a plain table';
  END IF;
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid = rel AND NOT tgisinternal) THEN
    RAISE EXCEPTION 'restore guard: public.hatamoto_instance has triggers';
  END IF;
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_rewrite WHERE ev_class = rel) THEN
    RAISE EXCEPTION 'restore guard: public.hatamoto_instance has rules';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_attribute
                 WHERE attrelid = rel AND attname = 'id' AND NOT attisdropped
                   AND atttypid = 'pg_catalog.text'::pg_catalog.regtype) THEN
    RAISE EXCEPTION 'restore guard: public.hatamoto_instance.id is not text';
  END IF;
END
$guard$;
`

// writeTemp0600 writes data to a new 0600 file named after name in dir and
// returns its path.
func writeTemp0600(dir, name string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, name)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := os.Chmod(path, 0o600); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}
