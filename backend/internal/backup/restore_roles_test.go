package backup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const coreListing = `;
; Archive created at 2026-01-01 00:00:00 UTC
2605; 0 0 ACL - SCHEMA public pg_database_owner
222; 1255 16398 FUNCTION public f() hm_src
2606; 0 0 ACL public FUNCTION f() hm_src
217; 1259 16392 TABLE public hatamoto_instance hm_src
2607; 0 0 ACL public TABLE hatamoto_instance hm_src
2580; 0 16392 TABLE DATA public hatamoto_instance hm_src
2054; 826 16403 DEFAULT ACL public DEFAULT PRIVILEGES FOR TABLES hm_src
2052; 826 16390 DEFAULT ACL - DEFAULT PRIVILEGES FOR FUNCTIONS hm_src`

func TestSourceAppRole(t *testing.T) {
	if got := sourceAppRole(coreListing); got != "hm_src" {
		t.Fatalf("sourceAppRole = %q", got)
	}
	if got := sourceAppRole("2580; 0 16392 TABLE DATA public hatamoto_instance hm_src"); got != "" {
		t.Fatalf("a data line is not the table: %q", got)
	}
}

func TestExcludeACLAndListing(t *testing.T) {
	kept := excludeACL(coreListing)
	for _, l := range strings.Split(kept, "\n") {
		if strings.Contains(l, " ACL ") {
			t.Fatalf("ACL line survived: %q", l)
		}
	}
	if !strings.Contains(kept, "FUNCTION public f() hm_src") || !strings.Contains(kept, "TABLE DATA") {
		t.Fatalf("object lines lost:\n%s", kept)
	}
	core := aclOnlyList(coreListing, true)
	if !strings.Contains(core, "DEFAULT ACL public") || strings.Contains(core, "DEFAULT ACL - ") {
		t.Fatalf("core ACL list keeps schema-scoped and drops global default ACL:\n%s", core)
	}
	if n := len(strings.Split(core, "\n")); n != 4 {
		t.Fatalf("core ACL list has %d lines:\n%s", n, core)
	}
	if !strings.Contains(aclOnlyList(coreListing, false), "DEFAULT ACL - ") {
		t.Fatal("a non-core list keeps global default ACL")
	}
}

func TestStmtRoles(t *testing.T) {
	for _, c := range []struct {
		stmt string
		want []string
	}{
		{`GRANT SELECT(id) ON TABLE public.t2 TO reporting;`, []string{"reporting"}},
		{`GRANT ALL ON TABLE public.t TO a, "Odd Role";`, []string{"a", "Odd Role"}},
		{`ALTER DEFAULT PRIVILEGES FOR ROLE hm_src IN SCHEMA public GRANT SELECT ON TABLES TO reporting;`, []string{"hm_src", "reporting"}},
		{`REVOKE ALL ON FUNCTION public.f() FROM PUBLIC;`, nil},
		{`GRANT SELECT ON TABLE public.t TO x WITH GRANT OPTION GRANTED BY y;`, []string{"x", "y"}},
		{`GRANT EXECUTE ON FUNCTION public.f(a integer, b text) TO "to, from";`, []string{"to, from"}},
		{`GRANT SELECT ON TABLE public."a TO b" TO r1;`, []string{"r1"}},
		{`ALTER DEFAULT PRIVILEGES FOR ROLE hm_src REVOKE ALL ON FUNCTIONS FROM PUBLIC;`, []string{"hm_src"}},
	} {
		if got := stmtRoles(c.stmt); !slices.Equal(got, c.want) {
			t.Errorf("stmtRoles(%s) = %q, want %q", c.stmt, got, c.want)
		}
	}
}

func TestRewriteStmt(t *testing.T) {
	for _, c := range []struct{ stmt, want string }{
		{`GRANT SELECT ON TABLE public.t TO hm_src, reporting;`, `GRANT SELECT ON TABLE public.t TO hm_dst, reporting;`},
		{`ALTER DEFAULT PRIVILEGES FOR ROLE hm_src IN SCHEMA public GRANT SELECT ON TABLES TO hm_src;`,
			`ALTER DEFAULT PRIVILEGES FOR ROLE hm_dst IN SCHEMA public GRANT SELECT ON TABLES TO hm_dst;`},
		{`GRANT SELECT ON TABLE public.hm_src TO "hm_src";`, `GRANT SELECT ON TABLE public.hm_src TO hm_dst;`},
		{`GRANT SELECT ON TABLE public.t TO other;`, `GRANT SELECT ON TABLE public.t TO other;`},
	} {
		if got := rewriteStmt(c.stmt, "hm_src", "hm_dst"); got != c.want {
			t.Errorf("rewriteStmt(%s) = %s, want %s", c.stmt, got, c.want)
		}
	}
	if got := rewriteStmt(`GRANT SELECT ON TABLE t TO a;`, "a", "Odd Role"); got != `GRANT SELECT ON TABLE t TO "Odd Role";` {
		t.Errorf("target is quoted when needed: %s", got)
	}
}

// aclRig answers pg_restore for analyzeRoles: -l gives a listing, -f - the
// SQL of the dump named by the last argument.
func aclRig(listing string, sql map[string]string, calls *[]string) ExecFunc {
	return func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		dump := filepath.Base(args[len(args)-1])
		if slices.Contains(args, "-l") {
			return []byte(listing), nil
		}
		return []byte(sql[dump]), nil
	}
}

func TestAnalyzeRolesMapsAndSkips(t *testing.T) {
	work := t.TempDir()
	var calls []string
	x := &restorer{
		mf: manifest{Plugins: []string{"polls"}, Roles: []string{"hm_p_abcd1234_polls", "hatamoto_role_admin_abcd1234"}},
		run: aclRig(coreListing, map[string]string{
			"core.dump": "\\restrict abc\nSET x = 1;\n" +
				"GRANT SELECT ON TABLE public.t TO hm_src;\n" +
				"GRANT SELECT ON TABLE public.t TO reporting;\n" +
				"GRANT SELECT ON TABLE public.t TO gone, PUBLIC;\n" +
				"GRANT SELECT ON TABLE public.t TO hm_p_abcd1234_polls;\n" +
				"ALTER DEFAULT PRIVILEGES FOR ROLE hm_src IN SCHEMA public GRANT SELECT ON TABLES TO reporting;\n" +
				"REVOKE ALL ON FUNCTION public.f() FROM PUBLIC;\n",
			"plugin_polls.dump": "GRANT SELECT ON TABLE plugin_polls.p TO hm_src;\nGRANT SELECT ON TABLE plugin_polls.p TO gone2;\n",
		}, &calls),
	}
	q := &engineQuerier{existingRoles: []string{"reporting"}}
	a, err := analyzeRoles(context.Background(), x, q, work, "hm_dst")
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != "hm_src" || a.Target != "hm_dst" {
		t.Fatalf("mapping %q -> %q", a.Source, a.Target)
	}
	wantCore := []string{
		"GRANT SELECT ON TABLE public.t TO hm_dst;",
		"GRANT SELECT ON TABLE public.t TO reporting;",
		"GRANT SELECT ON TABLE public.t TO hm_p_abcd1234_polls;",
		"ALTER DEFAULT PRIVILEGES FOR ROLE hm_dst IN SCHEMA public GRANT SELECT ON TABLES TO reporting;",
		"REVOKE ALL ON FUNCTION public.f() FROM PUBLIC;",
	}
	if !slices.Equal(a.Core, wantCore) {
		t.Fatalf("core statements:\n%s\nwant:\n%s", strings.Join(a.Core, "\n"), strings.Join(wantCore, "\n"))
	}
	if got := a.Plugin["plugin_polls"]; !slices.Equal(got, []string{"GRANT SELECT ON TABLE plugin_polls.p TO hm_dst;"}) {
		t.Fatalf("plugin statements: %q", got)
	}
	if !slices.Equal(a.Skipped, []string{"gone", "gone2"}) {
		t.Fatalf("skipped = %q", a.Skipped)
	}
	entries, _ := os.ReadDir(work)
	if len(entries) != 0 {
		t.Fatalf("analysis left files behind: %v", entries)
	}
	for _, c := range calls {
		if strings.Contains(c, "-f -") && !strings.Contains(c, "--no-owner") {
			t.Fatalf("pg_restore reads the grants with --no-owner: %s", c)
		}
	}
}

func TestAnalyzeRolesUnknownSourceAndSameName(t *testing.T) {
	work := t.TempDir()
	var calls []string
	x := &restorer{run: aclRig("1; 0 0 TABLE public other hm_src", map[string]string{
		"core.dump": "GRANT SELECT ON TABLE public.t TO hm_src;\n",
	}, &calls)}
	a, err := analyzeRoles(context.Background(), x, &engineQuerier{}, work, "hm_dst")
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != "" || len(a.Core) != 0 || !slices.Equal(a.Skipped, []string{"hm_src"}) {
		t.Fatalf("unknown source: %+v", a)
	}
	x.run = aclRig(coreListing, map[string]string{"core.dump": "GRANT SELECT ON TABLE public.t TO hm_src;\n"}, &calls)
	a, err = analyzeRoles(context.Background(), x, &engineQuerier{}, work, "hm_src")
	if err != nil || len(a.Skipped) != 0 || len(a.Core) != 1 {
		t.Fatalf("same name: %+v, %v", a, err)
	}
}

func TestAnalyzeRolesRefusesMultiLineStatement(t *testing.T) {
	var calls []string
	x := &restorer{run: aclRig(coreListing, map[string]string{"core.dump": "GRANT SELECT ON TABLE public.t\n  TO x\n"}, &calls)}
	if _, err := analyzeRoles(context.Background(), x, &engineQuerier{}, t.TempDir(), "hm_dst"); err == nil || !strings.Contains(err.Error(), "spans lines") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckGrantStmt(t *testing.T) {
	for _, ok := range []string{
		`GRANT SELECT ON TABLE public.t TO hm_dst;`,
		`GRANT SELECT ON TABLE public.t TO "odd;\\role" WITH GRANT OPTION;`,
		`REVOKE ALL ON FUNCTION public.f() FROM PUBLIC;`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE hm_dst IN SCHEMA public GRANT SELECT ON TABLES TO reporting;`,
	} {
		if err := checkGrantStmt(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`GRANT SELECT ON TABLE public.t TO hm_src WITH GRANT OPTION; \! id ;`,
		`GRANT SELECT ON TABLE public.t TO hm_src \! id;`,
		`GRANT SELECT ON TABLE public.t TO hm_src; DROP TABLE public.t;`,
		`GRANT SELECT ON TABLE public.t TO "hm_src;`,
		`DROP TABLE public.t;`,
	} {
		if err := checkGrantStmt(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestAnalyzeRolesRefusesMetaCommandAndSecondStatement(t *testing.T) {
	for _, line := range []string{
		`GRANT SELECT ON TABLE public.t TO hm_src WITH GRANT OPTION; \! id ;`,
		`GRANT SELECT ON TABLE public.t TO hm_src; DROP TABLE public.t;`,
	} {
		var calls []string
		x := &restorer{run: aclRig(coreListing, map[string]string{"core.dump": line + "\n"}, &calls)}
		_, err := analyzeRoles(context.Background(), x, &engineQuerier{}, t.TempDir(), "hm_dst")
		if err == nil || !strings.Contains(err.Error(), "refusing the grants") {
			t.Errorf("%q: err = %v", line, err)
		}
	}
}
