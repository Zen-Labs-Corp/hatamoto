package wpimport

import (
	"html"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/core/internal/form"
)

// cf7MaxFields is the most fields one imported form keeps (the form editor's
// own limit).
const cf7MaxFields = 60

// CF7Mapped is a Contact Form 7 template turned into Hatamoto form fields.
type CF7Mapped struct {
	Fields []form.FormField
	// Submit is the label of the [submit] tag, "" when it had none.
	Submit string
	// Dropped lists the tags that were not carried over, as "type name".
	Dropped []string
	// Conditional is true when the template used conditional groups, whose
	// show/hide rules are not imported.
	Conditional bool
}

type cf7Token struct {
	text   string
	quoted bool
}

// cf7Tokens splits the inside of a tag on spaces, keeping quoted values whole.
func cf7Tokens(s string) []cf7Token {
	var out []cf7Token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"' || c == '\'':
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				out = append(out, cf7Token{text: s[i+1:], quoted: true})
				return out
			}
			out = append(out, cf7Token{text: s[i+1 : i+1+j], quoted: true})
			i += j + 2
		default:
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' && s[j] != '\r' {
				j++
			}
			out = append(out, cf7Token{text: s[i:j]})
			i = j
		}
	}
	return out
}

// cf7Tag is one parsed [type name options "values"] tag.
type cf7Tag struct {
	typ      string
	required bool
	name     string
	opts     map[string]string // "key:value" options
	flags    map[string]bool
	values   []string // quoted values, in order
	holder   string   // the value after the placeholder option
}

var cf7Flags = map[string]bool{"include_blank": true, "multiple": true, "first_as_label": true, "exclusive": true, "use_label_element": true,
	"label_first": true, "placeholder": true, "readonly": true, "akismet": true, "default": true}

func parseCF7Tag(body string) (cf7Tag, bool) {
	toks := cf7Tokens(body)
	if len(toks) == 0 || toks[0].quoted {
		return cf7Tag{}, false
	}
	t := cf7Tag{opts: map[string]string{}, flags: map[string]bool{}}
	typ := strings.ToLower(toks[0].text)
	t.required = strings.HasSuffix(typ, "*")
	typ = strings.TrimSuffix(typ, "*")
	if typ == "" || !unicode.IsLetter(rune(typ[0])) {
		return cf7Tag{}, false
	}
	for _, r := range typ {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return cf7Tag{}, false
		}
	}
	t.typ = typ
	for i, tok := range toks[1:] {
		switch {
		case tok.quoted:
			if i > 0 && toks[i].text == "placeholder" && !toks[i].quoted {
				t.holder = tok.text
			} else {
				t.values = append(t.values, tok.text)
			}
		case strings.Contains(tok.text, ":"):
			k, v, _ := strings.Cut(tok.text, ":")
			t.opts[strings.ToLower(k)] = v
		case cf7Flags[strings.ToLower(tok.text)]:
			t.flags[strings.ToLower(tok.text)] = true
		case t.name == "":
			t.name = tok.text
		}
	}
	return t, true
}

// cf7Label is the last non-empty line of the text before a tag.
func cf7Label(pending string) string {
	pending = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "<p>", "\n", "</p>", "\n").Replace(pending)
	lines := strings.Split(pending, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.Join(strings.Fields(stripTags(lines[i])), " ")
		if low := strings.ToLower(l); strings.HasSuffix(low, "(required)") {
			l = strings.TrimSpace(l[:len(l)-len("(required)")])
		}
		if l != "" {
			return l
		}
	}
	return ""
}

// cf7Name makes a form field name from a CF7 tag name.
func cf7Name(raw string, taken map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	n := b.String()
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "f_" + n
	}
	if len(n) > 64 {
		n = n[:64]
	}
	base := n
	for i := 2; taken[n]; i++ {
		suffix := "_" + strconv.Itoa(i)
		n = base
		if len(n)+len(suffix) > 64 {
			n = n[:64-len(suffix)]
		}
		n += suffix
	}
	taken[n] = true
	return n
}

func cf7Options(values []string) []form.FieldOption {
	var out []form.FieldOption
	for _, v := range values {
		label, val, found := strings.Cut(v, "|")
		if !found {
			val = label
		}
		out = append(out, form.FieldOption{Label: label, Value: val})
	}
	return out
}

// cf7MaxMB reads a limit: option ("2mb", "512kb", or bytes) as whole megabytes.
func cf7MaxMB(s string) *int {
	s = strings.ToLower(strings.TrimSpace(s))
	unit := int64(1)
	switch {
	case strings.HasSuffix(s, "mb"):
		unit, s = 1<<20, strings.TrimSuffix(s, "mb")
	case strings.HasSuffix(s, "kb"):
		unit, s = 1<<10, strings.TrimSuffix(s, "kb")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 || n > 1<<40 {
		return nil
	}
	mb := int((n*unit + 1<<20 - 1) >> 20)
	mb = max(1, min(mb, form.MaxFileUploadMB))
	return &mb
}

func cf7Accept(s string) []string {
	var out []string
	for _, ext := range strings.Split(strings.ToLower(s), "|") {
		ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
		if form.ValidAcceptExt(ext) && !hasString(out, ext) {
			out = append(out, ext)
		}
	}
	if len(out) == 0 {
		return []string{"pdf"}
	}
	return out
}

func cf7Int(s string) *int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

func cf7Dropped(t cf7Tag) string { return strings.TrimSpace(t.typ + " " + t.name) }

// MapCF7 reads a Contact Form 7 template: tags such as [text* your-name],
// labelled by the text before them. Tags with no Hatamoto field are listed in
// Dropped. The input is untrusted and bad markup never panics.
func MapCF7(tpl string) CF7Mapped {
	var res CF7Mapped
	taken := map[string]bool{}
	pending := ""
	for i := 0; i < len(tpl); {
		open := strings.IndexByte(tpl[i:], '[')
		if open < 0 {
			break
		}
		open += i
		end := strings.IndexByte(tpl[open+1:], ']')
		next := strings.IndexByte(tpl[open+1:], '[')
		if end < 0 || (next >= 0 && next < end) {
			pending += tpl[i : open+1]
			i = open + 1
			continue
		}
		end += open + 1
		pending += tpl[i:open]
		i = end + 1
		tag, ok := parseCF7Tag(tpl[open+1 : end])
		if !ok {
			pending += tpl[open : end+1]
			continue
		}
		label := cf7Label(pending)
		pending = ""
		inner := ""
		if tag.typ == "acceptance" {
			if k := strings.Index(tpl[i:], "[/acceptance]"); k >= 0 {
				inner = strings.Join(strings.Fields(stripTags(tpl[i:i+k])), " ")
				i += k + len("[/acceptance]")
			}
		}
		cf7AddTag(&res, tag, label, inner, taken)
	}
	cf7Validate(&res)
	return res
}

func cf7AddTag(res *CF7Mapped, t cf7Tag, label, inner string, taken map[string]bool) {
	if strings.HasPrefix(t.typ, "/") {
		return
	}
	var f form.FormField
	switch t.typ {
	case "submit":
		if res.Submit == "" && len(t.values) > 0 {
			res.Submit = t.values[0]
		}
		return
	case "group", "conditional", "cf7ic":
		res.Conditional = true
		res.Dropped = append(res.Dropped, cf7Dropped(t))
		return
	case "text", "email", "url", "number", "range", "date", "textarea", "tel":
		f.Type = map[string]string{"tel": form.FieldTypePhone, "range": form.FieldTypeNumber}[t.typ]
		if f.Type == "" {
			f.Type = t.typ
		}
		f.Placeholder = html.UnescapeString(t.holder)
		if len(t.values) > 0 {
			f.DefaultValue = t.values[0]
		}
		if v := cf7Validation(t); v != nil {
			f.Validation = v
		}
	case "select", "radio", "checkbox":
		f.Type = t.typ
		f.Options = cf7Options(t.values)
		if t.typ == "checkbox" && len(f.Options) <= 1 {
			f.Options = nil
		}
	case "file":
		f.Type = form.FieldTypeFile
		f.Validation = &form.FieldValidation{Accept: cf7Accept(t.opts["filetypes"]), MaxMB: cf7MaxMB(t.opts["limit"])}
	case "acceptance":
		f.Type, f.Required = form.FieldTypeConsent, true
		label = firstNonEmpty(inner, label)
		f.Text = label
	case "hidden":
		f.Type = form.FieldTypeHidden
		f.Hidden = &form.HiddenSpec{Source: "static"}
		if len(t.values) > 0 {
			f.Hidden.Value = t.values[0]
		}
	default:
		res.Dropped = append(res.Dropped, cf7Dropped(t))
		return
	}
	if len(res.Fields) >= cf7MaxFields {
		res.Dropped = append(res.Dropped, cf7Dropped(t)+" (too many fields)")
		return
	}
	f.Name = cf7Name(firstNonEmpty(t.name, t.typ), taken)
	f.Label = firstNonEmpty(label, t.name, f.Name)
	if f.Type == form.FieldTypeCheckbox && len(f.Options) == 0 && len(t.values) == 1 && label == "" {
		f.Label = t.values[0]
	}
	if utf8.RuneCountInString(f.Label) > 200 {
		f.Label = string([]rune(f.Label)[:200])
	}
	if f.Type != form.FieldTypeHidden && f.Type != form.FieldTypeConsent {
		f.Required = t.required
	}
	f.ID = "f" + strconv.Itoa(len(res.Fields)+1)
	f.Position = len(res.Fields)
	res.Fields = append(res.Fields, f)
}

func cf7Validation(t cf7Tag) *form.FieldValidation {
	v := &form.FieldValidation{MinLength: cf7Int(t.opts["minlength"]), MaxLength: cf7Int(t.opts["maxlength"])}
	if t.typ == "number" || t.typ == "range" {
		v.Min, v.Max = cf7Int(t.opts["min"]), cf7Int(t.opts["max"])
	}
	if v.MinLength == nil && v.MaxLength == nil && v.Min == nil && v.Max == nil {
		return nil
	}
	return v
}

// cf7Validate drops the fields the form definition checks refuse, so the
// rest of the form can still be created.
func cf7Validate(res *CF7Mapped) {
	errs := form.ValidateDefinitionStrict(res.Fields)
	if len(errs) == 0 {
		return
	}
	bad := map[string]bool{}
	for _, e := range errs {
		if e.FieldID != "" {
			bad[e.FieldID] = true
		}
	}
	kept := res.Fields[:0]
	for _, f := range res.Fields {
		if bad[f.ID] {
			res.Dropped = append(res.Dropped, strings.TrimSpace(f.Type+" "+f.Name))
			continue
		}
		f.Position = len(kept)
		kept = append(kept, f)
	}
	res.Fields = kept
}
