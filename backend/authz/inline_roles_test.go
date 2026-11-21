package authz_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inlineRole matches a hard-coded owner/editor decision in Go code: a role
// compared with a role constant or a string literal, a literal role list,
// requireOwner, RequireRole and the removed APIKey.CreatorCanAct method (the authzsubject helper of that name is the
// permitted way and does not match). Comparisons
// with nil and "" are presence checks and do not match.
var inlineRole = regexp.MustCompile(`(?i:role)\s*(==|!=)\s*(string\()?((user\.)?Role(Owner|Editor)\b|"[^"]+")|\[\]string\{"(owner|editor)"|requireOwner\(|RequireRole\(|\b(k|key|apiKey)\.CreatorCanAct\(`)

// templateRole matches a template reading the signed-in user's role.
var templateRole = regexp.MustCompile(`\.User\.Role`)

// allowedInlineRole is one line that may keep a role comparison. contains is a
// substring of the trimmed line; why says which kind of decision it is.
type allowedInlineRole struct{ file, contains, why string }

var inlineRoleAllow = []allowedInlineRole{
	{"internal/user/repository.go", "input.Role != RoleOwner ||", "domain: owners cannot have an access end (target user)"},
	{"internal/user/repository.go", "*input.Role != RoleOwner", "domain: last-owner demotion rule"},
	{"internal/auth/usertokens.go", "role == user.RoleOwner && accessEnds", "domain: invited role"},
	{"internal/auth/usertokens.go", "role == string(user.RoleOwner) && accessEnds", "domain: invited role"},
	{"internal/auth/api_keys.go", "OwnerOnlyScope(scope) && k.CreatorRole != \"owner\"", "key scope semantics, mirrored by the authz scope check"},
	{"internal/auth/service.go", "IsSuperadmin: u.Role == user.RoleOwner", "claims derivation (database superadmin context)"},
	{"internal/admin/authmw.go", "IsSuperadmin: u.Role == user.RoleOwner", "claims derivation (database superadmin context)"},
	{"internal/admin/users.go", "role == user.RoleOwner && u.Role != user.RoleOwner", "domain: target role against the caller's access end"},
	{"internal/admin/users.go", "&& role == user.RoleOwner:", "domain: target role against the caller's access end"},
	{"internal/server/handlers.go", "role == user.RoleOwner && !middleware.Can(", "domain: target role (the permission part asks authz)"},
	{"internal/server/handlers.go", "*input.Role == user.RoleOwner", "domain: target role"},
	{"internal/server/handlers.go", "target.Role != user.RoleOwner", "domain: target role"},
	{"internal/siteconfig/siteconfig.go", "return role == \"owner\"", "two-factor policy binding (data policy, not a permission)"},
	{"internal/admin/ui/ui.go", "c.Role == \"title\"", "table column role, not a user role"},
	{"internal/admin/pluginui.go", "return []string{\"owner\"}", "plugin manifest ui.roles default (SDK contract)"},
	{"internal/pluginhost/routes.go", "roles = []string{\"owner\"}", "plugin manifest route roles default (SDK contract)"},
	{"internal/mcpserver/server.go", "key.CreatorRole != \"owner\"", "refusal wording after authz already denied"},
	{"internal/middleware/authz_subject.go", "roles := []string{\"owner\"}", "trace tag of the REST gate, derived from the default authorizer"},
	{"internal/oidcsignin/mapuser.go", "u.Role == \"owner\"", "domain: an identity-provider email never links to an existing owner account (target user)"},
}

// inlineRoleHits returns the matching "file:line: text" lines of one file's
// source, skipping comment lines.
func inlineRoleHits(re *regexp.Regexp, src string) (lines []int, texts []string) {
	for i, l := range strings.Split(src, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "//") {
			continue
		}
		if re.MatchString(l) {
			lines = append(lines, i+1)
			texts = append(texts, l)
		}
	}
	return lines, texts
}

// TestNoInlineRoleComparisons keeps new owner/editor comparisons out of
// handlers, view models, registries, mail code and templates: a decision about
// what a user may do asks authz.Can. The allow-list holds the comparisons that
// are not a permission of the signed-in user.
func TestNoInlineRoleComparisons(t *testing.T) {
	used := make([]bool, len(inlineRoleAllow))
	walk := func(root string, re *regexp.Regexp, ext string) {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ext) || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel("..", path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			lines, texts := inlineRoleHits(re, string(b))
			for i, text := range texts {
				ok := false
				for j, a := range inlineRoleAllow {
					if a.file == rel && strings.Contains(text, a.contains) {
						used[j], ok = true, true
					}
				}
				if !ok {
					t.Errorf("%s:%d: inline role check %q: ask authz.Can through the capability helpers (admin can/viewer, middleware.Can, authzuser), or add an allow entry with a reason", rel, lines[i], text)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	walk("../internal", inlineRole, ".go")
	walk("../web/admin", templateRole, ".html")
	for j, a := range inlineRoleAllow {
		if !used[j] {
			t.Errorf("stale allow entry: %s %q (%s) matches no line", a.file, a.contains, a.why)
		}
	}
}

func TestInlineRolePatternCatchesForms(t *testing.T) {
	for _, l := range []string{
		`if u.Role == "owner" {`,
		`if role != "editor" {`,
		`if claims.Role == user.RoleOwner {`,
		`roles := []string{"owner"}`,
		`if !requireOwner(w, r) {`,
		`key.CreatorCanAct()`,
	} {
		if !inlineRole.MatchString(l) {
			t.Errorf("pattern misses %q", l)
		}
	}
	for _, l := range []string{
		`if sh.Role == "" {`,
		`if input.Role != nil {`,
		`!authzsubject.CreatorCanAct(ctx, key)`,
	} {
		if inlineRole.MatchString(l) {
			t.Errorf("pattern flags %q", l)
		}
	}
	if _, texts := inlineRoleHits(inlineRole, "// u.Role == \"owner\"\n\t// role != \"editor\""); len(texts) != 0 {
		t.Errorf("comment lines matched: %v", texts)
	}
}
