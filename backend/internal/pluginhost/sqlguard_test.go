package pluginhost

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestGuardSQL(t *testing.T) {
	ok := []struct {
		sql  string
		mode sqlMode
	}{
		{`SELECT * FROM leads WHERE id = $1`, sqlDML},
		{`select 1;`, sqlDML},
		{`INSERT INTO leads (email) VALUES ($1) ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email RETURNING id`, sqlDML},
		{`WITH x AS (SELECT 1) SELECT * FROM x`, sqlDML},
		{`SELECT 'copy; set_config; public' AS s, "role" FROM t -- RESET ROLE`, sqlDML},
		{`SELECT $tag$ ; pg_read_file $tag$`, sqlDML},
		{`SELECT E'it\'s; copy' `, sqlDML},
		{`WITH x AS (SELECT 1 AS a) INSERT INTO leads (id) SELECT a FROM x`, sqlDML},
		{`UPDATE leads SET role = 'x' WHERE id = 1`, sqlDML},
		{`CREATE TABLE leads (id serial PRIMARY KEY, email text)`, sqlMigration},
		{`CREATE UNIQUE INDEX CONCURRENTLY i ON leads (email)`, sqlMigration},
		{`alter table leads add column n int`, sqlMigration},
		{`DROP TABLE IF EXISTS leads`, sqlMigration},
		{`CREATE OR REPLACE VIEW v AS SELECT 1`, sqlMigration},
	}
	for _, c := range ok {
		if _, err := guardSQL(c.sql, c.mode); err != nil {
			t.Errorf("guardSQL(%q) = %v, want ok", c.sql, err)
		}
	}

	bad := []struct {
		name string
		sql  string
		mode sqlMode
	}{
		{"core table", `SELECT * FROM public.hatamoto_users`, sqlDML},
		{"query_to_xml", `SELECT query_to_xml('select set_config(''role'',''none'',true)', false, false, '')`, sqlDML},
		{"query_to_xmlschema", `SELECT query_to_xmlschema('select 1', false, false, '')`, sqlMigration},
		{"table_to_xml", `SELECT table_to_xml('t'::regclass, false, false, '')`, sqlDML},
		{"cursor_to_xml", `SELECT cursor_to_xml('c', 1, false, false, '')`, sqlDML},
		{"ts_stat", `SELECT * FROM ts_stat('select 1')`, sqlDML},
		{"ts_rewrite", `SELECT ts_rewrite('a'::tsquery, 'select 1')`, sqlDML},
		{"select into", `SELECT 1 AS id INTO TEMP TABLE t`, sqlDML},
		{"select into migration", `SELECT * INTO t2 FROM t`, sqlMigration},
		{"set role", `SET ROLE postgres`, sqlDML},
		{"set local role", `SET LOCAL ROLE postgres`, sqlDML},
		{"reset role", `RESET ROLE`, sqlDML},
		{"reset all", `RESET ALL`, sqlDML},
		{"set search_path", `SET search_path = public`, sqlDML},
		{"session authorization", `SET SESSION AUTHORIZATION postgres`, sqlDML},
		{"multi statement", `SELECT 1; SELECT * FROM leads`, sqlDML},
		{"multi statement reset", `SELECT 1; RESET ROLE`, sqlDML},
		{"leading reset", `RESET ROLE; SELECT 1`, sqlDML},
		{"copy", `COPY leads TO STDOUT`, sqlDML},
		{"copy program", `COPY leads FROM PROGRAM 'id'`, sqlDML},
		{"copy in cte", `WITH x AS (SELECT 1) COPY x TO STDOUT`, sqlDML},
		{"pg_read_file", `SELECT pg_read_file('/etc/passwd')`, sqlDML},
		{"pg_catalog qualified", `SELECT pg_catalog.pg_read_file('/etc/passwd')`, sqlDML},
		{"quoted pg_read_file", `SELECT "pg_read_file"('/etc/passwd')`, sqlDML},
		{"pg_ls_dir", `SELECT pg_ls_dir('.')`, sqlDML},
		{"dblink", `SELECT * FROM dblink('host=localhost', 'select 1') AS t(a int)`, sqlDML},
		{"dblink_connect", `SELECT dblink_connect('x')`, sqlDML},
		{"current_setting", `SELECT current_setting('role')`, sqlDML},
		{"set_config", `SELECT set_config('role', 'postgres', true)`, sqlDML},
		{"set_config quoted", `SELECT "SET_CONFIG"('role', 'postgres', true)`, sqlDML},
		{"unicode escape", `SELECT U&"set\005fconfig"('role','postgres',true)`, sqlDML},
		{"non-ascii", "SELECT \u0430bc", sqlDML},
		{"lo_import", `SELECT lo_import('/etc/passwd')`, sqlDML},
		{"information_schema", `SELECT * FROM information_schema.tables`, sqlDML},
		{"session_user", `SELECT session_user`, sqlDML},
		{"do block", `DO $$ BEGIN PERFORM 1; END $$`, sqlDML},
		{"call", `CALL x()`, sqlDML},
		{"execute", `EXECUTE foo`, sqlDML},
		{"prepare", `PREPARE foo AS SELECT 1`, sqlDML},
		{"listen", `LISTEN x`, sqlDML},
		{"begin", `BEGIN`, sqlDML},
		{"commit", `COMMIT`, sqlDML},
		{"explain", `EXPLAIN SELECT 1`, sqlDML},
		{"ddl at runtime", `CREATE TABLE t (id int)`, sqlDML},
		{"empty", ``, sqlDML},
		{"only comment", `-- nothing`, sqlDML},
		{"unterminated comment", `SELECT 1 /* x`, sqlDML},
		{"unterminated string", `SELECT 'x`, sqlDML},
		{"unterminated dollar", `SELECT $a$ x`, sqlDML},
		{"function with SET role", `CREATE FUNCTION f() RETURNS int LANGUAGE sql SET role = 'postgres' AS 'select 1'`, sqlMigration},
		{"create or replace function", `CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE sql AS 'select 1'`, sqlMigration},
		{"procedure", `CREATE PROCEDURE p() LANGUAGE sql AS 'select 1'`, sqlMigration},
		{"trigger", `CREATE TRIGGER t AFTER INSERT ON x EXECUTE FUNCTION f()`, sqlMigration},
		{"extension", `CREATE EXTENSION dblink`, sqlMigration},
		{"role", `CREATE ROLE evil SUPERUSER`, sqlMigration},
		{"alter role", `ALTER ROLE hm_plugin_x SUPERUSER`, sqlMigration},
		{"schema", `CREATE SCHEMA other`, sqlMigration},
		{"temp table", `CREATE TEMP TABLE t (id int)`, sqlMigration},
		{"policy", `CREATE POLICY p ON t USING (true)`, sqlMigration},
		{"grant", `GRANT ALL ON ALL TABLES IN SCHEMA public TO hm_plugin_x`, sqlMigration},
		{"set in migration", `SET ROLE postgres`, sqlMigration},
		{"alter table to public", `ALTER TABLE t SET SCHEMA public`, sqlMigration},
	}
	for _, c := range bad {
		_, err := guardSQL(c.sql, c.mode)
		if err == nil || !errors.Is(err, ErrSQLBlocked) {
			t.Errorf("%s: guardSQL(%q) = %v, want ErrSQLBlocked", c.name, c.sql, err)
		}
	}
}

func TestSplitSQLStatements(t *testing.T) {
	got, err := splitSQLStatements(`
-- +goose Up
CREATE TABLE a (id int); -- trailing ; comment
INSERT INTO a VALUES ('x;y'), (E'it\'s;');
/* ; */ SELECT $$ ; $$;
`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-- +goose Up\nCREATE TABLE a (id int)",
		"-- trailing ; comment\nINSERT INTO a VALUES ('x;y'), (E'it\\'s;')",
		"/* ; */ SELECT $$ ; $$",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split =\n%q\nwant\n%q", got, want)
	}
	if _, err := splitSQLStatements(`SELECT 'x`); err == nil {
		t.Fatal("unterminated string must fail")
	}
}

func TestGooseUp(t *testing.T) {
	src := "-- +goose Up\n-- +goose StatementBegin\nCREATE TABLE a (id int);\n-- +goose StatementEnd\n-- +goose Down\nDROP TABLE a;\n"
	if got := gooseUp(src); got == "" || contains(got, "DROP") || !contains(got, "CREATE TABLE") {
		t.Fatalf("gooseUp = %q", got)
	}
	if got := gooseUp("CREATE TABLE b (id int);"); !contains(got, "CREATE TABLE b") {
		t.Fatalf("an unannotated file is all Up: %q", got)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
