package pluginhost

import (
	"errors"
	"fmt"
	"strings"
)

// The SQL guard is defence in depth for plugin SQL. It is NOT the security
// boundary: that is the database. Plugin SQL runs on a pool whose session user
// is the plugin's own LOGIN role (plugindb_provision.go), a member of nothing
// that owns nothing outside its schema, so no statement, however it is
// written, can reach a core table or another plugin's schema. Tests run the
// whole escape suite with the guard switched off.
//
// The guard still keeps plugin SQL to one DML (or, in a migration, one
// table-shaped DDL) statement and refuses the functions that run a SQL string
// or reach outside the plugin's role, so that obvious abuse is stopped before
// anything is sent and gives a clear error.

// sqlMode selects what a plugin may run.
type sqlMode int

const (
	sqlDML       sqlMode = iota // db.query, db.exec: SELECT, INSERT, UPDATE, DELETE, WITH, VALUES
	sqlMigration                // DML plus CREATE/ALTER/DROP of tables, indexes, views, sequences, types
)

// ErrSQLBlocked is wrapped by every guard refusal.
var ErrSQLBlocked = errors.New("sql blocked")

var (
	dmlFirst = map[string]bool{"select": true, "insert": true, "update": true, "delete": true, "with": true, "values": true}
	ddlFirst = map[string]bool{"create": true, "alter": true, "drop": true}
	// ddlObject is the word after CREATE/ALTER/DROP (after UNIQUE or
	// MATERIALIZED, which are skipped). Functions, procedures, triggers,
	// extensions, roles, schemas, policies and the like are not here: a
	// function can carry a SET role attribute, a trigger or policy runs code.
	ddlObject = map[string]bool{"table": true, "index": true, "view": true, "sequence": true, "type": true}
	// bannedWord are words refused anywhere outside literals. Prefix matches
	// are in bannedPrefix.
	bannedWord = map[string]bool{
		"set_config": true, "current_setting": true, "copy": true, "public": true,
		"information_schema": true, "session_user": true,
		// large objects reach the server's files and other roles' data
		"lo_import": true, "lo_export": true, "lo_get": true, "lo_put": true, "lo_open": true,
		"lo_create": true, "lo_creat": true, "lo_unlink": true, "lo_from_bytea": true,
		"loread": true, "lowrite": true,
		// functions that run a SQL string, possibly several statements
		"ts_stat": true, "ts_rewrite": true,
	}
	bannedPrefix = []string{"pg_", "dblink", "query_to_xml", "cursor_to_xml", "table_to_xml", "schema_to_xml", "database_to_xml"}
)

// sqlToken is one lexical item outside comments and literals.
type sqlToken struct {
	word string // lower-cased word or quoted identifier; "" for punctuation
	semi bool   // a ';'
}

// lexSQL splits sql into tokens, ignoring comments and string literals, and
// refuses anything it cannot read with certainty (unterminated quotes or
// comments, Unicode-escaped identifiers).
func lexSQL(sql string) ([]sqlToken, error) {
	var toks []sqlToken
	n := len(sql)
	for i := 0; i < n; {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < n && sql[i+1] == '-':
			for i < n && sql[i] != '\n' && sql[i] != '\r' {
				i++
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			depth := 1
			i += 2
			for depth > 0 {
				switch {
				case i+1 >= n:
					return nil, fmt.Errorf("%w: unterminated comment", ErrSQLBlocked)
				case sql[i] == '/' && sql[i+1] == '*':
					depth++
					i += 2
				case sql[i] == '*' && sql[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
		case c == '\'':
			end, err := skipQuoted(sql, i, '\'', false)
			if err != nil {
				return nil, err
			}
			i = end
		case c == '"':
			end, err := skipQuoted(sql, i, '"', false)
			if err != nil {
				return nil, err
			}
			toks = append(toks, sqlToken{word: strings.ToLower(strings.ReplaceAll(sql[i+1:end-1], `""`, `"`))})
			i = end
		case c == '$':
			// $tag$ ... $tag$ (a string) or a $1 parameter.
			j := i + 1
			for j < n && (isWordByte(sql[j]) && sql[j] != '$') {
				j++
			}
			if j < n && sql[j] == '$' && (j == i+1 || !isDigit(sql[i+1])) {
				tag := sql[i : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					return nil, fmt.Errorf("%w: unterminated dollar quote", ErrSQLBlocked)
				}
				i = j + 1 + end + len(tag)
			} else {
				i = j
			}
		case isWordStart(c):
			j := i
			for j < n && isWordByte(sql[j]) {
				j++
			}
			word := strings.ToLower(sql[i:j])
			if j < n && (sql[j] == '\'' || sql[j] == '"' || (sql[j] == '&' && j+1 < n && (sql[j+1] == '\'' || sql[j+1] == '"'))) {
				switch word {
				case "e":
					// E'...' escape string: backslash escapes the quote.
					if sql[j] != '\'' {
						return nil, fmt.Errorf("%w: unexpected prefix %q", ErrSQLBlocked, word)
					}
					end, err := skipQuoted(sql, j, '\'', true)
					if err != nil {
						return nil, err
					}
					i = end
					continue
				case "b", "x", "n":
					if sql[j] == '\'' { // bit, hex and national strings
						end, err := skipQuoted(sql, j, '\'', false)
						if err != nil {
							return nil, err
						}
						i = end
						continue
					}
				case "u":
					return nil, fmt.Errorf("%w: Unicode escapes are not allowed", ErrSQLBlocked)
				}
			}
			toks = append(toks, sqlToken{word: word})
			i = j
		case c == ';':
			toks = append(toks, sqlToken{semi: true})
			i++
		case c >= 0x80:
			// Non-ASCII outside literals could spell a look-alike or a
			// unicode identifier; plugin SQL has no need for it.
			return nil, fmt.Errorf("%w: non-ASCII character outside a literal", ErrSQLBlocked)
		default:
			toks = append(toks, sqlToken{})
			i++
		}
	}
	return toks, nil
}

// skipQuoted returns the index after the quoted run that starts at sql[i]
// (the quote itself); doubled quotes escape, and so does a backslash when
// backslash is set.
func skipQuoted(sql string, i int, q byte, backslash bool) (int, error) {
	for j := i + 1; j < len(sql); j++ {
		switch {
		case backslash && sql[j] == '\\':
			j++
		case sql[j] == q:
			if j+1 < len(sql) && sql[j+1] == q {
				j++
				continue
			}
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("%w: unterminated quote", ErrSQLBlocked)
}

func isWordStart(c byte) bool { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }
func isWordByte(c byte) bool  { return isWordStart(c) || isDigit(c) || c == '$' }

// splitSQLStatements splits a migration body at top-level semicolons. Each
// statement is returned without its semicolon; empty ones are dropped. It
// shares the lexer's refusals.
func splitSQLStatements(sql string) ([]string, error) {
	if _, err := lexSQL(sql); err != nil {
		return nil, err
	}
	var out []string
	start := 0
	i := 0
	for i < len(sql) {
		// Re-scan with the lexer's own skipping so ';' inside literals and
		// comments never splits.
		c := sql[i]
		switch {
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			depth := 1
			i += 2
			for depth > 0 && i+1 < len(sql) {
				switch {
				case sql[i] == '/' && sql[i+1] == '*':
					depth++
					i += 2
				case sql[i] == '*' && sql[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
		case c == '\'' || c == '"':
			i, _ = skipQuoted(sql, i, c, false)
		case c == '$':
			j := i + 1
			for j < len(sql) && isWordByte(sql[j]) && sql[j] != '$' {
				j++
			}
			if j < len(sql) && sql[j] == '$' && (j == i+1 || !isDigit(sql[i+1])) {
				tag := sql[i : j+1]
				i = j + 1 + strings.Index(sql[j+1:], tag) + len(tag)
			} else {
				i = j
			}
		case isWordStart(c):
			j := i
			for j < len(sql) && isWordByte(sql[j]) {
				j++
			}
			if strings.EqualFold(sql[i:j], "e") && j < len(sql) && sql[j] == '\'' {
				i, _ = skipQuoted(sql, j, '\'', true)
			} else {
				i = j
			}
		case c == ';':
			if s := strings.TrimSpace(sql[start:i]); s != "" {
				out = append(out, s)
			}
			i++
			start = i
		default:
			i++
		}
	}
	if s := strings.TrimSpace(sql[start:]); s != "" {
		out = append(out, s)
	}
	return out, nil
}

// guardSQL checks one statement. It returns the statement ready to send
// (a single trailing semicolon removed), or an error wrapping ErrSQLBlocked.
func guardSQL(sql string, mode sqlMode) (string, error) {
	toks, err := lexSQL(sql)
	if err != nil {
		return "", err
	}
	// One statement: no ';' except as the very last token.
	for i, t := range toks {
		if t.semi && i != len(toks)-1 {
			return "", fmt.Errorf("%w: more than one statement", ErrSQLBlocked)
		}
	}
	if len(toks) > 0 && toks[len(toks)-1].semi {
		toks = toks[:len(toks)-1]
		sql = strings.TrimRight(strings.TrimSpace(sql), ";")
	}
	if len(toks) == 0 || toks[0].word == "" {
		return "", fmt.Errorf("%w: empty statement", ErrSQLBlocked)
	}
	first := toks[0].word
	switch {
	case dmlFirst[first]:
	case mode == sqlMigration && ddlFirst[first]:
		if !ddlAllowed(toks) {
			return "", fmt.Errorf("%w: only tables, indexes, views, sequences and types may be created, altered or dropped", ErrSQLBlocked)
		}
	default:
		return "", fmt.Errorf("%w: %q statements are not allowed", ErrSQLBlocked, first)
	}
	for i, t := range toks {
		w := t.word
		if w == "" {
			continue
		}
		if isBannedWord(w) {
			return "", fmt.Errorf("%w: %q is not allowed", ErrSQLBlocked, w)
		}
		// INTO is only INSERT INTO: SELECT ... INTO is DDL.
		if w == "into" && (i == 0 || toks[i-1].word != "insert") {
			return "", fmt.Errorf("%w: SELECT INTO is not allowed", ErrSQLBlocked)
		}
	}
	return sql, nil
}

func isBannedWord(w string) bool {
	if bannedWord[w] {
		return true
	}
	for _, p := range bannedPrefix {
		if strings.HasPrefix(w, p) {
			return true
		}
	}
	return false
}

// ddlAllowed reports whether a CREATE/ALTER/DROP names an allowed object.
func ddlAllowed(toks []sqlToken) bool {
	i := 1
	if toks[0].word == "create" && i < len(toks) && toks[i].word == "or" { // CREATE OR REPLACE VIEW
		if i+2 >= len(toks) || toks[i+1].word != "replace" || toks[i+2].word != "view" {
			return false
		}
		return true
	}
	for i < len(toks) && (toks[i].word == "unique" || toks[i].word == "materialized" || toks[i].word == "concurrently") {
		i++
	}
	return i < len(toks) && ddlObject[toks[i].word]
}
