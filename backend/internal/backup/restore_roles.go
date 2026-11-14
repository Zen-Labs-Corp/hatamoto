package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A dump names roles in its owner column (the restore ignores it: --no-owner
// and --role) and in the ACL and DEFAULT ACL entries. Those entries are not
// restored from the dump: restore lists leave them out, and the statements
// they stand for are read here, mapped to the target app role or dropped when
// they name a role that does not exist on the target, and run afterwards.

// roleAnalysis is what the dumps say about roles, seen from the target.
type roleAnalysis struct {
	Source, Target string // Source is "" when the dump does not tell
	Skipped        []string
	Core           []string            // surviving core statements, rewritten
	Plugin         map[string][]string // plugin schema -> surviving statements
}

var aclEntry = regexp.MustCompile(`^\d+; \d+ \d+ (DEFAULT )?ACL `)

// sourceAppRole is the owner of public.hatamoto_instance in a core dump
// listing: the app role of the instance the backup was taken from.
func sourceAppRole(coreListing string) string {
	for _, l := range strings.Split(coreListing, "\n") {
		f := strings.Fields(l)
		if len(f) >= 7 && f[3] == "TABLE" && f[4] == "public" && f[5] == "hatamoto_instance" {
			return f[len(f)-1]
		}
	}
	return ""
}

// excludeACL drops the ACL and DEFAULT ACL entries from a pg_restore -l
// listing; analyzeRoles turns them into statements.
func excludeACL(listing string) string {
	var keep []string
	for _, l := range strings.Split(listing, "\n") {
		if !aclEntry.MatchString(l) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// aclOnlyList keeps only the ACL and DEFAULT ACL entries of a listing. For the
// core dump the global DEFAULT ACL entries stay out: pauseOffsiteSQL sets them
// for the target app role.
func aclOnlyList(listing string, core bool) string {
	var keep []string
	for _, l := range strings.Split(listing, "\n") {
		if aclEntry.MatchString(l) && !(core && strings.Contains(l, " DEFAULT ACL - ")) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// roleRef is one role named in a statement, with its byte span in it.
type roleRef struct {
	Name       string
	Start, End int
}

type token struct {
	text       string
	start, end int
}

// tokenize splits a statement at white space and commas, keeping quoted
// identifiers and parenthesised argument lists in one piece.
func tokenize(s string) []token {
	var toks []token
	start := -1
	depth := 0
	inQuote := false
	flush := func(end int) {
		if start >= 0 {
			toks = append(toks, token{s[start:end], start, end})
			start = -1
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote:
			if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
			if start < 0 {
				start = i
			}
		case c == '(':
			depth++
			if start < 0 {
				start = i
			}
		case c == ')':
			if depth > 0 {
				depth--
			}
		case depth > 0:
		case c == ' ' || c == '\t':
			flush(i)
		case c == ',':
			flush(i)
			toks = append(toks, token{",", i, i + 1})
		default:
			if start < 0 {
				start = i
			}
		}
	}
	flush(len(s))
	return toks
}

func isWord(t token, words ...string) bool {
	for _, w := range words {
		if strings.EqualFold(t.text, w) {
			return true
		}
	}
	return false
}

// roleList reads "r1, r2, ..." from toks[i:] and returns the roles and the
// index after the list. PUBLIC is not a role.
func roleList(toks []token, i int) ([]roleRef, int) {
	var refs []roleRef
	for ; i < len(toks); i++ {
		t := toks[i]
		if t.text == "," {
			continue
		}
		if isWord(t, "WITH", "GRANTED", "CASCADE", "RESTRICT", "IN", "GRANT", "REVOKE") {
			break
		}
		if isWord(t, "PUBLIC") {
			continue
		}
		refs = append(refs, roleRef{unquoteIdent(t.text), t.start, t.end})
	}
	return refs, i
}

// stmtRefs finds the roles a GRANT, REVOKE or ALTER DEFAULT PRIVILEGES
// statement names: the grantees after its last TO or FROM, a GRANTED BY role
// and the roles of FOR ROLE.
func stmtRefs(stmt string) []roleRef {
	stmt = strings.TrimSuffix(strings.TrimSpace(stmt), ";")
	toks := tokenize(stmt)
	var refs []roleRef
	if len(toks) >= 5 && isWord(toks[0], "ALTER") && isWord(toks[1], "DEFAULT") && isWord(toks[2], "PRIVILEGES") &&
		isWord(toks[3], "FOR") && isWord(toks[4], "ROLE", "USER") {
		refs, _ = roleList(toks, 5)
	}
	last := -1
	for i, t := range toks {
		if isWord(t, "TO", "FROM") {
			last = i
		}
	}
	if last >= 0 {
		list, next := roleList(toks, last+1)
		refs = append(refs, list...)
		for j := next; j+2 < len(toks); j++ {
			if isWord(toks[j], "GRANTED") && isWord(toks[j+1], "BY") {
				refs = append(refs, roleRef{unquoteIdent(toks[j+2].text), toks[j+2].start, toks[j+2].end})
			}
		}
	}
	return refs
}

func unquoteIdent(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

var plainIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func quoteRole(name string) string {
	if plainIdent.MatchString(name) {
		return name
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// stmtRoles lists the roles a statement names, in order of appearance.
func stmtRoles(stmt string) []string {
	var names []string
	for _, r := range stmtRefs(stmt) {
		names = append(names, r.Name)
	}
	return names
}

// rewriteStmt replaces the role from by to wherever the statement names it.
func rewriteStmt(stmt, from, to string) string {
	refs := stmtRefs(stmt)
	for i := len(refs) - 1; i >= 0; i-- {
		if r := refs[i]; r.Name == from {
			stmt = stmt[:r.Start] + quoteRole(to) + stmt[r.End:]
		}
	}
	return stmt
}

// aclStatements runs pg_restore on the dump's ACL entries only and keeps the
// GRANT, REVOKE and ALTER DEFAULT PRIVILEGES lines. pg_dump writes each on one
// line; one that is not is an error, never a guess.
func (x *restorer) aclStatements(ctx context.Context, work, dump string, listing []byte, core bool) ([]string, error) {
	path := filepath.Join(work, dump)
	list, err := writeTemp0600(work, "acl-*.list", []byte(aclOnlyList(string(listing), core)+"\n"))
	if err != nil {
		return nil, err
	}
	defer os.Remove(list)
	out, err := x.run(ctx, nil, "pg_restore", "--no-owner", "-L", list, "-f", "-", path)
	if err != nil {
		return nil, fmt.Errorf("read the grants of %s: %s", dump, restoreError(out, err))
	}
	var stmts []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimRight(l, "\r ")
		if !strings.HasPrefix(l, "GRANT ") && !strings.HasPrefix(l, "REVOKE ") && !strings.HasPrefix(l, "ALTER DEFAULT PRIVILEGES ") {
			continue
		}
		if !strings.HasSuffix(l, ";") {
			return nil, fmt.Errorf("cannot read the grants of %s: statement %q spans lines", dump, capLine(l))
		}
		if err := checkGrantStmt(l); err != nil {
			return nil, fmt.Errorf("refusing the grants of %s: %w", dump, err)
		}
		stmts = append(stmts, l)
	}
	return stmts, nil
}

// checkGrantStmt accepts only what pg_dump writes for an ACL entry: one GRANT,
// REVOKE or ALTER DEFAULT PRIVILEGES statement ending in its only semicolon,
// with no backslash, comment opener, dollar sign or E-string outside a quoted
// identifier or literal (pg_dump writes none of them in an ACL line). The archive is
// untrusted input; anything else makes the restore refuse.
func checkGrantStmt(stmt string) error {
	if !strings.HasPrefix(stmt, "GRANT ") && !strings.HasPrefix(stmt, "REVOKE ") && !strings.HasPrefix(stmt, "ALTER DEFAULT PRIVILEGES ") {
		return fmt.Errorf("statement %q is not a GRANT, REVOKE or ALTER DEFAULT PRIVILEGES", capLine(stmt))
	}
	var quote byte
	for i := 0; i < len(stmt); i++ {
		c := stmt[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' && i > 0 && (stmt[i-1] == 'E' || stmt[i-1] == 'e'):
			return fmt.Errorf("statement %q holds an escape string", capLine(stmt))
		case c == '"' || c == '\'':
			quote = c
		case c == '$' || c == '-' && i+1 < len(stmt) && stmt[i+1] == '-' || c == '/' && i+1 < len(stmt) && stmt[i+1] == '*':
			return fmt.Errorf("statement %q holds a comment or dollar quote outside quotes", capLine(stmt))
		case c == '\\':
			return fmt.Errorf("statement %q holds a backslash outside quotes", capLine(stmt))
		case c == ';' && i != len(stmt)-1:
			return fmt.Errorf("statement %q holds more than one statement", capLine(stmt))
		}
	}
	if quote != 0 || !strings.HasSuffix(stmt, ";") {
		return fmt.Errorf("statement %q is not one complete statement", capLine(stmt))
	}
	return nil
}

// analyzeRoles reads the grants of the extracted dumps in work and sorts them
// for the target: the source app role becomes target, a role that exists (or
// will, after the bootstrap and plugin provisioning) stays, any other makes
// its statement a skip.
func analyzeRoles(ctx context.Context, x *restorer, q Querier, work, target string) (*roleAnalysis, error) {
	a := &roleAnalysis{Target: target, Plugin: map[string][]string{}}
	listing, err := x.run(ctx, nil, "pg_restore", "-l", filepath.Join(work, coreName))
	if err != nil {
		return nil, fmt.Errorf("list %s: %s", coreName, restoreError(listing, err))
	}
	a.Source = sourceAppRole(string(listing))
	core, err := x.aclStatements(ctx, work, coreName, listing, true)
	if err != nil {
		return nil, err
	}
	plugin := map[string][]string{}
	for _, name := range x.mf.Plugins {
		schema := schemaName(name)
		pl, err := x.run(ctx, nil, "pg_restore", "-l", filepath.Join(work, schema+".dump"))
		if err != nil {
			return nil, fmt.Errorf("list %s.dump: %s", schema, restoreError(pl, err))
		}
		if plugin[schema], err = x.aclStatements(ctx, work, schema+".dump", pl, false); err != nil {
			return nil, err
		}
	}

	// Candidates for the pg_roles lookup: every role left after the mapping.
	known := func(r string) bool { return r == target || slices.Contains(x.mf.Roles, r) }
	remap := func(stmt string) string {
		if a.Source != "" {
			return rewriteStmt(stmt, a.Source, target)
		}
		return stmt
	}
	unknown := map[string]bool{}
	for _, stmts := range append([][]string{core}, mapValues(plugin)...) {
		for _, s := range stmts {
			for _, r := range stmtRoles(remap(s)) {
				if !known(r) {
					unknown[r] = true
				}
			}
		}
	}
	exist, err := existingRoles(ctx, q, unknown)
	if err != nil {
		return nil, err
	}
	skipped := map[string]bool{}
	sift := func(stmts []string) []string {
		var keep []string
		for _, s := range stmts {
			s = remap(s)
			ok := true
			for _, r := range stmtRoles(s) {
				if !known(r) && !exist[r] {
					skipped[r] = true
					ok = false
				}
			}
			if ok {
				keep = append(keep, s)
			}
		}
		return keep
	}
	a.Core = sift(core)
	for schema, stmts := range plugin {
		a.Plugin[schema] = sift(stmts)
	}
	for r := range skipped {
		a.Skipped = append(a.Skipped, r)
	}
	sort.Strings(a.Skipped)
	return a, nil
}

func mapValues(m map[string][]string) [][]string {
	var out [][]string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// existingRoles says which of names exist in the cluster.
func existingRoles(ctx context.Context, q Querier, names map[string]bool) (map[string]bool, error) {
	have := map[string]bool{}
	if len(names) == 0 {
		return have, nil
	}
	var asked []string
	for n := range names {
		asked = append(asked, n)
	}
	rows, err := q.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname = ANY($1)`, asked)
	if err != nil {
		return nil, fmt.Errorf("cannot look up roles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		have[n] = true
	}
	return have, rows.Err()
}
