package staticexport

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/redirect"
)

// HTAccessFile is the Apache per-directory configuration file the SFTP target
// keeps current. It is never part of the manifest.
const HTAccessFile = ".htaccess"

const (
	htaccessBegin = "# BEGIN Hatamoto"
	htaccessEnd   = "# END Hatamoto"
)

// RenderHTAccess renders the Hatamoto block of .htaccess, or nil when there is
// no rule to write. Exact lines become mod_alias RedirectMatch rules, anchored
// with an optional trailing slash: a plain Redirect would match by prefix, so
// /blog -> /news would also catch /blog/post-1. Wildcard rules become
// mod_rewrite RewriteRule lines in per-directory form (no leading slash). An
// exact line that a wildcard rule would also match is repeated as a
// RewriteRule ahead of the wildcards: mod_rewrite runs before mod_alias in
// .htaccess, and exact rules must keep winning. warnings name every rule that
// was skipped.
func RenderHTAccess(exact []Line, patterns []*redirect.Redirect) (block []byte, warnings []string) {
	lines := slices.Clone(exact)
	slices.SortStableFunc(lines, func(a, b Line) int { return strings.Compare(a.From, b.From) })

	sorted := slices.Clone(patterns)
	redirect.SortPatterns(sorted)
	var rules []string // RewriteRule lines of the wildcard rules, in first-match order
	var sources []string
	for _, r := range sorted {
		if !redirect.ValidStatusCode(r.StatusCode) {
			warnings = append(warnings, fmt.Sprintf("redirect rule %s: status %d cannot be written to .htaccess", r.SourcePath, r.StatusCode))
			continue
		}
		re, to, ok := htaccessPattern(r.SourcePath, r.TargetURL)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("redirect rule %s -> %s cannot be written to .htaccess, skipped", r.SourcePath, r.TargetURL))
			continue
		}
		rules = append(rules, rewriteRule(re, to, r.StatusCode))
		sources = append(sources, r.SourcePath)
	}

	var alias, rewrite []string
	for _, l := range lines {
		switch {
		case !redirect.ValidStatusCode(l.Status):
			warnings = append(warnings, fmt.Sprintf("redirect %s: status %d cannot be written to .htaccess", l.From, l.Status))
			continue
		case !plainToken(l.From) || !plainToken(l.To) || !strings.HasPrefix(l.From, "/") || strings.ContainsAny(l.From, "?#"):
			warnings = append(warnings, fmt.Sprintf("redirect %s: cannot be written to .htaccess, skipped", l.From))
			continue
		}
		from := strings.TrimSuffix(l.From, "/")
		re := regexp.QuoteMeta(from) + "/?$"
		to := htaccessTarget(l.To)
		alias = append(alias, "RedirectMatch "+strconv.Itoa(l.Status)+" ^"+re+" "+to)
		for _, src := range sources {
			if _, hit := redirect.MatchPattern(src, l.From); hit {
				rp := strings.TrimPrefix(regexp.QuoteMeta(from), "/")
				if rp == "" {
					rewrite = append(rewrite, rewriteRule("$", to, l.Status))
				} else {
					rewrite = append(rewrite, rewriteRule(rp+"/?$", to, l.Status))
				}
				break
			}
		}
	}
	rewrite = append(rewrite, rules...)
	if len(alias) == 0 && len(rewrite) == 0 {
		return nil, warnings
	}

	var b bytes.Buffer
	b.WriteString(htaccessBegin + "\n")
	b.WriteString("# Written by the static export. Lines between these markers are replaced on every export.\n")
	if len(alias) > 0 {
		b.WriteString("<IfModule mod_alias.c>\n" + strings.Join(alias, "\n") + "\n</IfModule>\n")
	}
	if len(rewrite) > 0 {
		b.WriteString("<IfModule mod_rewrite.c>\nRewriteEngine On\n" + strings.Join(rewrite, "\n") + "\n</IfModule>\n")
	}
	b.WriteString(htaccessEnd + "\n")
	return b.Bytes(), warnings
}

// rewriteRule renders one per-directory RewriteRule; re is anchored here and has no leading slash.
func rewriteRule(re, to string, status int) string {
	return "RewriteRule ^" + re + " " + to + " [R=" + strconv.Itoa(status) + ",L]"
}

// htaccessTarget escapes a redirect target for the Apache regexp substitution
// syntax, where $ and \ are special.
func htaccessTarget(to string) string {
	return strings.NewReplacer(`\`, `\\`, `$`, `\$`).Replace(to)
}

// htaccessPattern turns a wildcard rule into a per-directory RewriteRule
// regexp and substitution: "/a/*" + "/b/$1" -> `a/([^/]+)/?$` "/b/$1". It
// refuses an invalid or unsafe rule, a $n with no matching wildcard and a
// target mod_rewrite would re-escape.
func htaccessPattern(src, dst string) (re, to string, ok bool) {
	if redirect.ValidatePatternRule(src, dst) != nil || !redirect.IsPatternSource(src) ||
		!redirect.SafeLocation(dst, true) || !plainToken(dst) || strings.Contains(dst, "%") {
		return "", "", false
	}
	segs := strings.Split(strings.Trim(src, "/"), "/")
	var parts []string
	caps := 0
	for i, s := range segs {
		switch s {
		case "*":
			caps++
			parts = append(parts, `([^/]+)`)
		case "**":
			if i != len(segs)-1 {
				return "", "", false
			}
			caps++
			if i == 0 {
				parts = append(parts, `(.*)`)
			} else {
				parts[i-1] += `(?:/(.*))?`
			}
		default:
			parts = append(parts, regexp.QuoteMeta(s))
		}
	}
	if caps > 9 {
		return "", "", false
	}
	for i := 0; i+1 < len(dst); i++ {
		if dst[i] == '$' && dst[i+1] >= '1' && dst[i+1] <= '9' {
			if int(dst[i+1]-'0') > caps {
				return "", "", false
			}
			i++
		}
	}
	return strings.Join(parts, "/") + "/?$", dst, true
}

// MergeHTAccess replaces the Hatamoto block in remote with block. An absent
// block is appended (after a newline when remote does not end with one); an
// empty block removes the existing block and its trailing newline. Every other
// byte of remote is kept. A BEGIN without END, an END before BEGIN or two
// blocks is an error: the file is left for the owner to fix.
func MergeHTAccess(remote, block []byte) ([]byte, error) {
	empty := len(bytes.TrimSpace(block)) == 0
	begin, end := -1, -1 // byte offsets of the BEGIN line start and the byte after the END line
	var begins, ends int
	var endStart int
	for off := 0; off < len(remote); {
		nl := bytes.IndexByte(remote[off:], '\n')
		next := len(remote)
		if nl >= 0 {
			next = off + nl + 1
		}
		switch string(bytes.TrimRight(remote[off:next], "\r\n")) {
		case htaccessBegin:
			begins++
			if begin < 0 {
				begin = off
			}
		case htaccessEnd:
			ends++
			if end < 0 {
				endStart, end = off, next
			}
		}
		off = next
	}
	switch {
	case begins == 0 && ends == 0:
		if empty {
			return remote, nil
		}
		out := slices.Clone(remote)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		return append(out, withNewline(block)...), nil
	case begins != 1 || ends != 1:
		return nil, errors.New(".htaccess has an unbalanced Hatamoto block; fix or remove the " + htaccessBegin + " / " + htaccessEnd + " lines")
	case endStart < begin:
		return nil, errors.New(".htaccess has the Hatamoto block markers in the wrong order; fix or remove them")
	}
	out := slices.Clone(remote[:begin])
	if !empty {
		out = append(out, withNewline(block)...)
	}
	return append(out, remote[end:]...), nil
}

func withNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(slices.Clone(b), '\n')
	}
	return b
}
