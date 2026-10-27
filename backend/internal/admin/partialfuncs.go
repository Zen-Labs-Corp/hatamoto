package admin

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// required makes a missing mandatory partial argument a render error instead of
// silently producing an unlabeled control.
func required(what string, v any) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("%s is required", what)
	}
	if s, ok := v.(string); ok && s == "" {
		return nil, fmt.Errorf("%s is required", what)
	}
	return v, nil
}

// calloutKind maps a flash-style or callout kind (a string from a dict or a Go
// struct) to the callout vocabulary: info (default), success, warning, danger.
func calloutKind(kind any) string {
	k, _ := kind.(string)
	switch k {
	case "success", "warning", "danger":
		return k
	case "error":
		return "danger"
	}
	return "info"
}

// calloutIcon is the glyph for a callout kind.
func calloutIcon(kind any) string {
	switch calloutKind(kind) {
	case "success":
		return "check-circle"
	case "warning":
		return "alert"
	case "danger":
		return "alert-circle"
	}
	return "info"
}

// describedBy joins the ids of the help and error elements a control points at.
func describedBy(id string, help, errMsg any) string {
	var ids []string
	if s, _ := help.(string); s != "" {
		ids = append(ids, id+"-help")
	}
	if s, _ := errMsg.(string); s != "" {
		ids = append(ids, id+"-error")
	}
	return strings.Join(ids, " ")
}

// allowedAttr is the whitelist of extra attributes a partial caller may add:
// htmx (minus inline-code and header/value injection), data-*, aria-*, form
// association and a few plain ones.
var allowedAttr = regexp.MustCompile(`^(hx-(get|post|put|patch|delete|target|swap|trigger|select|push-url|indicator|disabled-elt|include|params|sync|encoding|ext|disinherit|inherit|preserve)|data-[a-z0-9-]+|aria-[a-z-]+|form|formaction|formmethod|formnovalidate|formtarget|formenctype|disabled|name|value|id|title|autofocus|autocomplete|autocapitalize|autocorrect|spellcheck|inputmode|maxlength|minlength|pattern|min|max|step|readonly|rel|target|accept|popovertarget|popovertargetaction|hidden|download|list)$`)

// attrs renders a caller-supplied attribute map as safe HTML attributes. An
// attribute outside the whitelist is an error, never silently dropped.
func attrs(m map[string]string) (template.HTMLAttr, error) {
	if len(m) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if !allowedAttr.MatchString(k) {
			return "", fmt.Errorf("attribute %q is not allowed on a partial", k)
		}
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(html.EscapeString(m[k]))
		b.WriteByte('"')
	}
	return template.HTMLAttr(b.String()), nil //nolint:gosec // names whitelisted, values escaped
}

// sentence turns an error message written for logs ("password must be at least
// 12 characters") into one for people: capital first letter, closing full stop.
func sentence(msg string) string {
	return fullStop(upperFirst(msg))
}

func upperFirst(msg string) string {
	if msg == "" {
		return msg
	}
	r := []rune(msg)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// fullStop closes msg with a full stop unless it already ends a sentence. It
// keeps the case of the first letter, for text that follows a colon.
func fullStop(msg string) string {
	if msg == "" || strings.ContainsAny(msg[len(msg)-1:], ".!?") {
		return msg
	}
	return msg + "."
}
