package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/urlpattern"
)

func (a *Admin) mountSchemas(authed *httpx.Group) {
	owner := authed.Group("", a.RequireRole("owner"))
	owner.HandleFunc("GET /settings/types", a.handleSchemas)
	owner.HandleFunc("GET /settings/types/new", a.handleSchemaNew)
	owner.HandleFunc("POST /settings/types", a.handleSchemaCreate)
	owner.HandleFunc("GET /settings/types/preview-pattern", a.handlePatternPreview)
	owner.HandleFunc("GET /settings/types/{id}", a.handleSchemaEdit)
	owner.HandleFunc("POST /settings/types/{id}", a.handleSchemaSave)
	owner.HandleFunc("GET /settings/types/{id}/delete", a.handleSchemaDeleteConfirm)
	owner.HandleFunc("POST /settings/types/{id}/delete", a.handleSchemaDelete)
}

// ---- list -------------------------------------------------------------

type schemasData struct {
	Actions []ui.Button
	Table   ui.Table
}

func (a *Admin) handleSchemas(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Entity types")
		return
	}
	ctx := r.Context()
	list, err := c.Entities.ListSchemas(ctx)
	if err != nil {
		a.serverError(w, r, "The entity types could not be loaded", err)
		return
	}
	t := ui.Table{
		ID: "schemas", Caption: "Entity types", BaseURL: a.adminPath + "/settings/types", CSRF: csrfToken(ctx),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "slug", Label: "Slug"}, {Key: "fields", Label: "Fields", Align: "right"}, {Key: "entities", Label: "Entities", Align: "right"}, {Key: "url", Label: "Address pattern"}},
		Empty:   ui.Empty{Icon: "file-text", Title: "No entity types yet", Body: "An entity type describes the fields of a kind of content.", Actions: []ui.Button{{Label: "New entity type", Variant: "primary", Href: a.adminPath + "/settings/types/new"}}},
	}
	for _, s := range list {
		id := s.ID.String()
		count := 0
		if res, err := c.Entities.ListEntitiesWithOffset(ctx, &entity.ListEntitiesFilter{SchemaID: &s.ID}, &entity.OffsetPage{Page: 1, Limit: 1}); err == nil {
			count = res.Total
		}
		name := ui.Cell{Text: s.Name, Link: a.adminPath + "/settings/types/" + id}
		pattern := "Default"
		if st, err := s.URLSettings(); err == nil && st.URLPattern != "" {
			pattern = st.URLPattern
		}
		row := ui.Row{ID: id, Label: s.Name, Cells: []ui.Cell{name, {Text: s.Slug}, {Text: strconv.Itoa(len(fieldRows(s.Schema)))}, {Text: strconv.Itoa(count)}, {Text: pattern}}}
		if s.IsBuiltin {
			row.Cells[1] = ui.Cell{Text: s.Slug + " (built in)"}
			row.Actions = []ui.Action{{Label: "View", Href: a.adminPath + "/settings/types/" + id}}
		} else {
			row.Actions = []ui.Action{{Label: "Edit", Href: a.adminPath + "/settings/types/" + id}, {Label: "Delete", Href: a.adminPath + "/settings/types/" + id + "/delete", Post: a.adminPath + "/settings/types/" + id + "/delete", Danger: true,
				ConfirmLabel: "Delete entity type", Confirm: "“" + s.Name + "” will be deleted. This only works while no entities use it."}}
		}
		t.Rows = append(t.Rows, row)
	}
	v := a.NewView(w, r, "Entity types")
	v.Data = schemasData{Actions: []ui.Button{{Label: "New entity type", Variant: "primary", Href: a.adminPath + "/settings/types/new", Icon: "plus"}}, Table: t}
	a.Render(w, r, http.StatusOK, "schemas/list", v)
}

// ---- field model --------------------------------------------------------

type schemaField struct {
	Key, Name, Kind, Title, Help, Options, Ref, Item, Raw string
	Required                                              bool
	Err                                                   string
	Open                                                  bool
	// Core marks a field of the built-in person type: it keeps its name and kind.
	Core bool
}

// LockAttrs are the attributes of the field-name input.
func (f schemaField) LockAttrs() map[string]string {
	if f.Core {
		return map[string]string{"autocomplete": "off", "readonly": "readonly"}
	}
	return map[string]string{"autocomplete": "off"}
}

// KindLabel names the field's kind.
func (f schemaField) KindLabel() string {
	for _, k := range fieldKinds {
		if k.Value == f.Kind {
			return k.Label
		}
	}
	return "Custom"
}

type kindDef struct{ Value, Label string }

// fieldKinds are the kinds the editor builds; "custom" is a property it keeps
// as it is.
var fieldKinds = []kindDef{
	{"text", "Text (one line)"}, {"textarea", "Text (several lines)"}, {"richtext", "Rich text"}, {"html", "HTML"},
	{"number", "Number"}, {"integer", "Whole number"}, {"boolean", "Yes / no"}, {"select", "Choice from a list"},
	{"image", "Image"}, {"url", "Web address"}, {"tags", "Tags (list of words)"}, {"reference", "Link to other entities"}, {"repeater", "Repeating group (list)"},
}

// itemKinds are the kinds a repeater's items can have.
var itemKinds = []kindDef{{"textarea", "Text"}, {"number", "Number"}, {"boolean", "Yes / no"}, {"image", "Image"}, {"url", "Web address"}}

func isKind(list []kindDef, v string) bool {
	return slices.ContainsFunc(list, func(k kindDef) bool { return k.Value == v })
}

// orderedProps decodes an object preserving key order.
func orderedProps(raw json.RawMessage) ([]string, map[string]json.RawMessage) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, nil
	}
	var keys []string
	m := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			break
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			break
		}
		if _, dup := m[k]; !dup {
			keys = append(keys, k)
		}
		m[k] = v
	}
	return keys, m
}

// classify maps a property to an editor kind.
func classify(p map[string]any) string {
	typ, _ := p["type"].(string)
	xhm, _ := p["x-hm-field"].(string)
	format, _ := p["format"].(string)
	media, _ := p["x-media"].(bool)
	switch typ {
	case "string":
		switch {
		case p["enum"] != nil:
			return "select"
		case media || xhm == "image":
			return "image"
		case xhm == "html":
			return "html"
		case xhm == "richtext" || format == "richtext":
			return "richtext"
		case format == "textarea" || xhm == "code" || xhm == "css":
			if xhm == "code" || xhm == "css" {
				return "custom"
			}
			return "textarea"
		case format == "uri" || xhm == "url":
			return "url"
		case xhm != "" && xhm != "plaintext":
			return "custom"
		}
		return "text"
	case "number":
		return "number"
	case "integer":
		return "integer"
	case "boolean":
		return "boolean"
	case "array":
		if _, ok := p["x-entity-schema"].(string); ok {
			return "reference"
		}
		items, _ := p["items"].(map[string]any)
		if it, _ := items["type"].(string); it == "string" && items["properties"] == nil && items["enum"] == nil && classify(items) == "text" {
			return "tags"
		}
		if items != nil {
			if k := classify(items); isKind(itemKinds, k) {
				return "repeater"
			}
		}
	}
	return "custom"
}

// fieldRows reads the properties of a schema into editor rows.
func fieldRows(schema json.RawMessage) []schemaField {
	keys, m := orderedProps(schema)
	var required []string
	if raw, ok := m["required"]; ok {
		_ = json.Unmarshal(raw, &required)
	}
	var out []schemaField
	if rawProps, ok := m["properties"]; ok {
		pk, pm := orderedProps(rawProps)
		var order []string
		if raw, ok := m["x-hm-order"]; ok {
			_ = json.Unmarshal(raw, &order)
		}
		pk = schemaform.SortNames(pk, order)
		for i, k := range pk {
			var p map[string]any
			_ = json.Unmarshal(pm[k], &p)
			f := schemaField{Key: "r" + strconv.Itoa(i), Name: k, Kind: classify(p), Raw: string(pm[k]), Required: slices.Contains(required, k)}
			f.Title, _ = p["title"].(string)
			f.Help, _ = p["description"].(string)
			if enum, ok := p["enum"].([]any); ok {
				var lines []string
				for _, e := range enum {
					lines = append(lines, fmt.Sprint(e))
				}
				f.Options = strings.Join(lines, "\n")
			}
			f.Ref, _ = p["x-entity-schema"].(string)
			if f.Kind == "repeater" {
				items, _ := p["items"].(map[string]any)
				f.Item = classify(items)
			}
			out = append(out, f)
		}
	}
	_ = keys
	return out
}

var fieldNameRx = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func nextKey(rows []schemaField) string {
	n := 0
	for _, r := range rows {
		if strings.HasPrefix(r.Key, "r") {
			if i, err := strconv.Atoi(r.Key[1:]); err == nil && i >= n {
				n = i + 1
			}
		}
	}
	return "r" + strconv.Itoa(n)
}

// readFieldRows reads the posted field list, in posted (DOM) order.
func readFieldRows(r *http.Request) []schemaField {
	var rows []schemaField
	seen := map[string]bool{}
	for _, k := range r.PostForm["field"] {
		if seen[k] || !regexp.MustCompile(`^r[0-9]{1,4}$`).MatchString(k) {
			continue
		}
		seen[k] = true
		g := func(s string) string { return strings.TrimSpace(r.PostFormValue("f." + k + "." + s)) }
		rows = append(rows, schemaField{
			Key: k, Name: g("name"), Kind: g("kind"), Title: g("title"), Help: g("help"), Options: r.PostFormValue("f." + k + ".options"),
			Ref: g("ref"), Item: g("item"), Raw: r.PostFormValue("f." + k + ".raw"), Required: r.PostFormValue("f."+k+".required") == "true",
		})
	}
	return rows
}

// applySchemaFieldOp applies an add/up/down/remove button to rows.
func applySchemaFieldOp(rows []schemaField, op, addKind string) ([]schemaField, bool) {
	switch {
	case op == "add":
		if !isKind(fieldKinds, addKind) {
			addKind = "text"
		}
		return append(rows, schemaField{Key: nextKey(rows), Kind: addKind, Open: true}), true
	case strings.HasPrefix(op, "up:"), strings.HasPrefix(op, "down:"), strings.HasPrefix(op, "remove:"):
		verb, key, _ := strings.Cut(op, ":")
		i := slices.IndexFunc(rows, func(f schemaField) bool { return f.Key == key })
		if i < 0 {
			return rows, true
		}
		switch verb {
		case "up":
			rows[i].Open = true
			if i > 0 {
				rows[i-1], rows[i] = rows[i], rows[i-1]
			}
		case "down":
			rows[i].Open = true
			if i < len(rows)-1 {
				rows[i+1], rows[i] = rows[i], rows[i+1]
			}
		case "remove":
			rows = slices.Delete(rows, i, i+1)
		}
		return rows, true
	}
	return rows, false
}

// kindKeys are the keys that make up a field's kind (its type, format, choices
// and so on); constraintKeys only make sense for some types. When a field's
// kind changes both go; when it does not, nothing the editor did not edit is
// touched.
var (
	kindKeys       = []string{"type", "format", "enum", "items", "x-hm-field", "x-media", "x-entity-schema"}
	constraintKeys = []string{"default", "const", "minLength", "maxLength", "pattern", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minItems", "maxItems", "uniqueItems"}
)

// decodeProperty reads a property keeping numbers exact.
func decodeProperty(raw string) map[string]any {
	base := map[string]any{}
	if raw == "" {
		return base
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&base); err != nil || base == nil {
		return map[string]any{}
	}
	return base
}

// optionLines lists an enum one choice per line, as the editor shows it.
func optionLines(p map[string]any) string {
	enum, _ := p["enum"].([]any)
	lines := make([]string, 0, len(enum))
	for _, e := range enum {
		lines = append(lines, fmt.Sprint(e))
	}
	return strings.Join(lines, "\n")
}

// scalarInto sets the keys of a scalar kind on into.
func scalarInto(kind string, into map[string]any) {
	switch kind {
	case "text":
		into["type"] = "string"
	case "textarea":
		into["type"], into["format"] = "string", "textarea"
	case "richtext":
		into["type"], into["format"] = "string", "richtext"
	case "html":
		into["type"], into["x-hm-field"] = "string", "html"
	case "number":
		into["type"] = "number"
	case "integer":
		into["type"] = "integer"
	case "boolean":
		into["type"] = "boolean"
	case "image":
		into["type"], into["format"], into["x-media"] = "string", "uri", true
	case "url":
		into["type"], into["format"] = "string", "uri"
	}
}

// buildProperty merges the edits of one row into the property it had, so every
// keyword the editor does not show (format, items.*, maxLength, pattern, enum
// values, x-*) survives a save. Only a changed kind rewrites the type keys.
func buildProperty(f schemaField) (json.RawMessage, string) {
	base := decodeProperty(f.Raw)
	origKind := ""
	if f.Raw != "" {
		origKind = classify(base)
	}
	marshal := func() (json.RawMessage, string) {
		setText(base, "title", f.Title)
		setText(base, "description", f.Help)
		b, _ := json.Marshal(base)
		return b, ""
	}
	if f.Kind == "custom" {
		return marshal()
	}
	if !isKind(fieldKinds, f.Kind) {
		return nil, "Choose a field kind"
	}
	changed := f.Kind != origKind
	if changed {
		for _, k := range kindKeys {
			delete(base, k)
		}
		for _, k := range constraintKeys {
			delete(base, k)
		}
	}
	switch f.Kind {
	case "select":
		if changed || f.Options != optionLines(base) {
			var opts []any
			seen := map[string]bool{}
			for _, line := range strings.Split(f.Options, "\n") {
				if o := strings.TrimSpace(line); o != "" && !seen[o] {
					seen[o] = true
					opts = append(opts, o)
				}
			}
			if len(opts) == 0 {
				return nil, "Add at least one choice, one per line"
			}
			base["type"], base["enum"] = "string", opts
		}
	case "tags":
		if changed {
			base["type"], base["items"] = "array", map[string]any{"type": "string"}
		}
	case "reference":
		if f.Ref == "" {
			return nil, "Choose which entity type this links to"
		}
		if changed {
			base["type"], base["items"] = "array", map[string]any{"type": "string"}
		}
		base["x-entity-schema"] = f.Ref
	case "repeater":
		if !isKind(itemKinds, f.Item) {
			return nil, "Choose what each item holds"
		}
		items, _ := base["items"].(map[string]any)
		if changed || items == nil || classify(items) != f.Item {
			item := map[string]any{}
			scalarInto(f.Item, item)
			base["type"], base["items"] = "array", item
		}
	default:
		if changed {
			scalarInto(f.Kind, base)
		}
	}
	return marshal()
}

func setText(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	} else {
		delete(m, k)
	}
}

// buildSchema writes the rows over base (the schema being edited), keeping its
// other top-level keys. Row errors are set on the rows.
func buildSchema(base json.RawMessage, rows []schemaField) (json.RawMessage, bool) {
	ok := true
	seen := map[string]bool{}
	var props bytes.Buffer
	var required []string
	props.WriteByte('{')
	for i := range rows {
		f := &rows[i]
		f.Err = ""
		switch {
		case !fieldNameRx.MatchString(f.Name):
			f.Err = "Use letters, digits and underscores, starting with a letter"
		case seen[f.Name]:
			f.Err = "Two fields cannot share a name"
		}
		var prop json.RawMessage
		if f.Err == "" {
			var msg string
			if prop, msg = buildProperty(*f); msg != "" {
				f.Err = msg
			}
		}
		if f.Err != "" {
			f.Open, ok = true, false
			continue
		}
		seen[f.Name] = true
		if props.Len() > 1 {
			props.WriteByte(',')
		}
		kb, _ := json.Marshal(f.Name)
		props.Write(kb)
		props.WriteByte(':')
		props.Write(prop)
		if f.Required {
			required = append(required, f.Name)
		}
	}
	props.WriteByte('}')
	if !ok {
		return nil, false
	}
	keys, m := orderedProps(base)
	if m == nil {
		keys, m = nil, map[string]json.RawMessage{}
	}
	m["type"] = json.RawMessage(`"object"`)
	m["properties"] = props.Bytes()
	names := make([]string, 0, len(rows))
	for _, f := range rows {
		names = append(names, f.Name)
	}
	m["x-hm-order"], _ = json.Marshal(names)
	if len(required) > 0 {
		m["required"], _ = json.Marshal(required)
	} else {
		delete(m, "required")
	}
	for _, k := range []string{"type", "properties", "required", "x-hm-order"} {
		if _, has := m[k]; has && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for _, k := range keys {
		v, has := m[k]
		if !has {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		out.Write(kb)
		out.WriteByte(':')
		out.Write(v)
	}
	out.WriteByte('}')
	return out.Bytes(), true
}

// ---- settings ------------------------------------------------------------

type schemaSettingsForm struct {
	URLPattern, ArchivePath, SitemapGroup, Aliases, ArchiveTitle, ArchiveCrumbLabel, ArchiveDescription, SchemaOrgType, Editor, Priority, ChangeFreq string
	Exclude, SectionPages                                                                                                                            bool
}

func settingsFormFrom(raw json.RawMessage) schemaSettingsForm {
	var st entity.SchemaURLSettings
	_ = json.Unmarshal(raw, &st)
	f := schemaSettingsForm{
		URLPattern: st.URLPattern, ArchivePath: st.ArchivePath, SitemapGroup: st.SitemapGroup, Aliases: strings.Join(st.Aliases, ", "),
		ArchiveTitle: st.ArchiveTitle, ArchiveCrumbLabel: st.ArchiveCrumbLabel, ArchiveDescription: st.ArchiveDescription, SchemaOrgType: st.SchemaOrgType, Editor: string(st.Editor),
		ChangeFreq: st.Sitemap.ChangeFreq, Exclude: st.Sitemap.Exclude, SectionPages: st.SectionPages,
	}
	if st.Sitemap.Priority != nil {
		f.Priority = strconv.FormatFloat(*st.Sitemap.Priority, 'f', -1, 64)
	}
	return f
}

func settingsFormFromPost(r *http.Request) schemaSettingsForm {
	g := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	return schemaSettingsForm{
		URLPattern: g("url_pattern"), ArchivePath: g("archive_path"), SitemapGroup: g("sitemap_group"), Aliases: g("aliases"),
		ArchiveTitle: g("archive_title"), ArchiveCrumbLabel: g("archive_crumb_label"), ArchiveDescription: g("archive_description"), SchemaOrgType: g("schema_org_type"), Editor: g("editor"),
		Priority: g("priority"), ChangeFreq: g("changefreq"), Exclude: r.PostFormValue("sitemap_exclude") == "true", SectionPages: r.PostFormValue("section_pages") == "true",
	}
}

var changeFreqs = []string{"", "always", "hourly", "daily", "weekly", "monthly", "yearly", "never"}

// settingsJSON writes the form over the existing settings (keeping keys the
// form does not edit, such as overrides) and checks it.
func (s schemaSettingsForm) settingsJSON(base json.RawMessage) (json.RawMessage, map[string]string) {
	errs := map[string]string{}
	m := map[string]json.RawMessage{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &m)
		if m == nil {
			m = map[string]json.RawMessage{}
		}
	}
	setStr := func(k, v string) {
		if v == "" {
			delete(m, k)
		} else {
			m[k], _ = json.Marshal(v)
		}
	}
	setStr("url_pattern", s.URLPattern)
	setStr("archive_path", s.ArchivePath)
	setStr("sitemap_group", s.SitemapGroup)
	setStr("archive_title", s.ArchiveTitle)
	setStr("archive_crumb_label", s.ArchiveCrumbLabel)
	setStr("archive_description", s.ArchiveDescription)
	setStr("schema_org_type", s.SchemaOrgType)
	setStr("editor", s.Editor)
	var aliases []string
	for _, a := range strings.Split(s.Aliases, ",") {
		if a = strings.TrimSpace(a); a != "" {
			aliases = append(aliases, a)
		}
	}
	if len(aliases) > 0 {
		m["aliases"], _ = json.Marshal(aliases)
	} else {
		delete(m, "aliases")
	}
	if s.SectionPages {
		m["section_pages"] = json.RawMessage("true")
	} else {
		delete(m, "section_pages")
	}
	sm := map[string]any{}
	if s.Exclude {
		sm["exclude"] = true
	}
	if s.ChangeFreq != "" {
		if !slices.Contains(changeFreqs, s.ChangeFreq) {
			errs["changefreq"] = "Choose one of the listed values"
		}
		sm["changefreq"] = s.ChangeFreq
	}
	if s.Priority != "" {
		p, err := strconv.ParseFloat(s.Priority, 64)
		if err != nil || p < 0 || p > 1 {
			errs["priority"] = "Enter a number from 0 to 1"
		} else {
			sm["priority"] = p
		}
	}
	if len(sm) > 0 {
		m["sitemap"], _ = json.Marshal(sm)
	} else {
		delete(m, "sitemap")
	}
	if s.URLPattern != "" {
		if _, err := urlpattern.Parse(s.URLPattern); err != nil {
			errs["url_pattern"] = patternMessage(err)
		}
	}
	if s.ArchivePath != "" && !strings.HasPrefix(s.ArchivePath, "/") {
		errs["archive_path"] = "Start with a slash, for example /blog"
	}
	if s.SchemaOrgType != "" && !entity.IsAllowedSchemaOrgType(s.SchemaOrgType) {
		errs["schema_org_type"] = "Choose one of the listed types"
	}
	if s.Editor != "" && s.Editor != entity.EditorPost {
		errs["editor"] = "Choose Form or Post editor"
	}
	out, _ := json.Marshal(m)
	if len(errs) == 0 {
		if err := entity.ValidateSchemaSettingsForWrite(out); err != nil {
			msg := sentence(strings.TrimPrefix(err.Error(), entity.ErrInvalidSchema.Error()+": "))
			errs[settingsFormKey(err)] = msg
		}
	}
	return out, errs
}

// settingsFormKey is the form field a schema settings error belongs to. The
// settings the form has no field of their own for are shown at the pattern.
func settingsFormKey(err error) string {
	var se *entity.SettingsError
	if errors.As(err, &se) {
		switch se.Field {
		case "archive_title", "archive_crumb_label", "archive_description", "archive_path", "aliases", "schema_org_type", "editor":
			return se.Field
		}
	}
	return "url_pattern"
}

// patternMessage is a pattern error for people.
func patternMessage(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, `": `); i >= 0 {
		msg = msg[i+3:]
	}
	return sentence(msg)
}

// handlePatternPreview answers the live preview next to the pattern field.
func (a *Admin) handlePatternPreview(w http.ResponseWriter, r *http.Request) {
	pat := strings.TrimSpace(r.URL.Query().Get("url_pattern"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if pat == "" {
		fmt.Fprint(w, `<span class="hm-muted">Built-in address: /{slug}, or /blog/{slug} for posts.</span>`)
		return
	}
	p, err := urlpattern.Parse(pat)
	if err != nil {
		fmt.Fprintf(w, `<span class="hm-field__error">%s</span>`, html.EscapeString(patternMessage(err)))
		return
	}
	now := time.Now().In(a.siteLocation(r.Context()))
	v := urlpattern.Vars{Slug: "example-page", Parent: "section", Path: "section/example-page", Year: now.Format("2006"), Month: now.Format("01"), Day: now.Format("02")}
	out, err := urlpattern.Render(p, v)
	if err != nil {
		fmt.Fprintf(w, `<span class="hm-field__error">%s</span>`, html.EscapeString(patternMessage(err)))
		return
	}
	fmt.Fprintf(w, `Example: <code>%s</code>`, html.EscapeString(out))
}

// ---- editor ----------------------------------------------------------------

type schemaForm struct {
	New, Builtin     bool
	ReadOnly         bool // a built-in type a site cannot change at all
	ID               string
	Name, Slug, Desc string
	Fields           []schemaField
	Settings         schemaSettingsForm
	JSON             string
	JSONErr          string
	Errors           map[string]string
	Summary          []ui.FieldError
	Kinds, Items     []kindDef
	Refs             []string
	SchemaOrgTypes   []string
	ChangeFreqs      []string
	Entities         int
	Actions          []ui.Button
	AddKind          string
	FocusOp          string // the move button to focus again after a reorder
	// BuiltinPostEditor is a built-in post type: it always opens the post editor.
	BuiltinPostEditor bool
}

// LockAttrs are the attributes of the name input: a built-in type keeps its name.
func (f *schemaForm) LockAttrs() map[string]string {
	if f.Builtin && !f.ReadOnly {
		return map[string]string{"autocomplete": "off", "readonly": "readonly"}
	}
	return map[string]string{"autocomplete": "off"}
}

// LockOnly is LockAttrs for a control that has no autocomplete.
func (f *schemaForm) LockOnly() map[string]string {
	if f.Builtin && !f.ReadOnly {
		return map[string]string{"readonly": "readonly"}
	}
	return nil
}

// lockBuiltin sets Builtin, ReadOnly and the Core rows for a built-in schema.
func (f *schemaForm) lockBuiltin(s *entity.EntitySchema) {
	f.Builtin = s.IsBuiltin
	f.BuiltinPostEditor = postEditors[s.Slug]
	f.ReadOnly = s.IsBuiltin && s.Slug != entity.PersonSchemaSlug
	if !f.Builtin || f.ReadOnly {
		return
	}
	for i := range f.Fields {
		f.Fields[i].Core = slices.Contains(entity.PersonCoreFields, f.Fields[i].Name)
	}
}

func (a *Admin) renderSchemaForm(w http.ResponseWriter, r *http.Request, status int, f *schemaForm) {
	f.Kinds, f.Items, f.SchemaOrgTypes, f.ChangeFreqs = fieldKinds, itemKinds, entity.AllowedSchemaOrgTypes, changeFreqs[1:]
	if list, err := a.deps.Content.Entities.ListSchemas(r.Context()); err == nil {
		for _, s := range list {
			f.Refs = append(f.Refs, s.Slug)
		}
	}
	if f.Errors == nil {
		f.Errors = map[string]string{}
	}
	for _, k := range []struct{ id, label string }{{"name", "Name"}, {"slug", "Slug"}, {"url_pattern", "Address pattern"}, {"archive_path", "Archive address"}, {"aliases", "Other slugs"}, {"priority", "Priority"}, {"changefreq", "Change frequency"}, {"schema_org_type", "Structured data type"}, {"editor", "Editor"}, {"archive_title", "Archive title"}, {"archive_crumb_label", "Breadcrumb label"}, {"archive_description", "Archive description"}, {"schema_json", "JSON schema"}} {
		if m := f.Errors[k.id]; m != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	for _, fld := range f.Fields {
		if fld.Err != "" {
			f.Summary = append(f.Summary, ui.FieldError{ID: "f-" + fld.Key + "-name", Label: "Field " + fld.Name, Message: fld.Err})
		}
	}
	title := f.Name
	if f.New {
		title = "New entity type"
	} else if title == "" {
		title = "Edit entity type"
	}
	v := a.NewView(w, r, title)
	v.Data = f
	v.Width = "narrow"
	v.Crumbs = a.areaCrumbs("types", Crumb{Label: title})
	a.Render(w, r, status, "schemas/edit", v)
}

func (a *Admin) handleSchemaNew(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.Entities == nil {
		a.unavailable(w, r, "Entity types")
		return
	}
	a.renderSchemaForm(w, r, http.StatusOK, &schemaForm{New: true, JSON: "{\n  \"type\": \"object\",\n  \"properties\": {}\n}"})
}

func (a *Admin) loadSchema(w http.ResponseWriter, r *http.Request) (*entity.EntitySchema, bool) {
	c := a.deps.Content
	id, ok := uuidParam(r, "id")
	if !ok || c.Entities == nil {
		a.notFound(w, r, "Entity type", a.adminPath+"/settings/types", "Back to entity types")
		return nil, false
	}
	s, err := c.Entities.GetSchema(r.Context(), id)
	if err != nil {
		if errors.Is(err, entity.ErrSchemaNotFound) {
			a.notFound(w, r, "Entity type", a.adminPath+"/settings/types", "Back to entity types")
		} else {
			a.serverError(w, r, "The entity type could not be loaded", err)
		}
		return nil, false
	}
	return s, true
}

func prettyJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

func (a *Admin) handleSchemaEdit(w http.ResponseWriter, r *http.Request) {
	s, ok := a.loadSchema(w, r)
	if !ok {
		return
	}
	desc := ""
	if s.Description != nil {
		desc = *s.Description
	}
	f := &schemaForm{ID: s.ID.String(), Name: s.Name, Slug: s.Slug, Desc: desc, Fields: fieldRows(s.Schema), Settings: settingsFormFrom(s.Settings), JSON: prettyJSON(s.Schema)}
	f.lockBuiltin(s)
	if res, err := a.deps.Content.Entities.ListEntitiesWithOffset(r.Context(), &entity.ListEntitiesFilter{SchemaID: &s.ID}, &entity.OffsetPage{Page: 1, Limit: 1}); err == nil {
		f.Entities = res.Total
	}
	a.renderSchemaForm(w, r, http.StatusOK, f)
}

// schemaFormErr maps a write error onto the form; false when it is not fixable there.
func schemaFormErr(f *schemaForm, err error) bool {
	switch {
	case errors.Is(err, entity.ErrSlugAlreadyExists):
		f.Errors["slug"] = "Another entity type already uses that slug"
	case errors.Is(err, entity.ErrInvalidSchema):
		msg := sentence(strings.TrimPrefix(err.Error(), entity.ErrInvalidSchema.Error()+": "))
		var se *entity.SettingsError
		switch {
		case errors.As(err, &se):
			f.Errors[settingsFormKey(err)] = msg
		default:
			f.Errors["schema_json"] = msg
			f.JSONErr = msg
		}
	default:
		return false
	}
	return true
}

func (a *Admin) readSchemaForm(r *http.Request, existing *entity.EntitySchema) *schemaForm {
	_ = r.ParseForm()
	f := &schemaForm{
		New: existing == nil, Name: strings.TrimSpace(r.PostFormValue("name")), Slug: strings.TrimSpace(r.PostFormValue("slug")), Desc: strings.TrimSpace(r.PostFormValue("description")),
		Fields: readFieldRows(r), Settings: settingsFormFromPost(r), JSON: r.PostFormValue("schema_json"), Errors: map[string]string{}, AddKind: r.PostFormValue("add_kind"),
	}
	if existing != nil {
		f.ID, f.Slug = existing.ID.String(), existing.Slug
		if existing.IsBuiltin {
			f.Name, f.Desc = existing.Name, ""
			if existing.Description != nil {
				f.Desc = *existing.Description
			}
		}
		f.lockBuiltin(existing)
	}
	if f.Name == "" {
		f.Errors["name"] = "Enter a name"
	} else if len([]rune(f.Name)) > 255 {
		f.Errors["name"] = "Use at most 255 characters"
	}
	return f
}

func (a *Admin) handleSchemaCreate(w http.ResponseWriter, r *http.Request) {
	c := a.deps.Content
	if c.Entities == nil {
		a.unavailable(w, r, "Entity types")
		return
	}
	f := a.readSchemaForm(r, nil)
	op := r.PostFormValue("op")
	if rows, structural := applySchemaFieldOp(f.Fields, op, f.AddKind); structural {
		f.Fields = rows
		f.Errors = map[string]string{}
		if strings.HasPrefix(op, "up:") || strings.HasPrefix(op, "down:") {
			f.FocusOp = op
		}
		a.renderSchemaForm(w, r, http.StatusOK, f)
		return
	}
	a.saveSchema(w, r, f, nil, op == "save_json")
}

func (a *Admin) handleSchemaSave(w http.ResponseWriter, r *http.Request) {
	s, ok := a.loadSchema(w, r)
	if !ok {
		return
	}
	if s.IsBuiltin && s.Slug != entity.PersonSchemaSlug {
		a.ErrorBack(w, r, http.StatusForbidden, "Built-in type", "Built-in entity types cannot be changed.", a.adminPath+"/settings/types/"+s.ID.String(), "Back")
		return
	}
	f := a.readSchemaForm(r, s)
	op := r.PostFormValue("op")
	if rows, structural := applySchemaFieldOp(f.Fields, op, f.AddKind); structural {
		f.Fields = rows
		f.Errors = map[string]string{}
		if strings.HasPrefix(op, "up:") || strings.HasPrefix(op, "down:") {
			f.FocusOp = op
		}
		a.renderSchemaForm(w, r, http.StatusOK, f)
		return
	}
	a.saveSchema(w, r, f, s, op == "save_json")
}

// saveSchema creates or updates from the posted form: the field list, or the
// JSON when useJSON.
func (a *Admin) saveSchema(w http.ResponseWriter, r *http.Request, f *schemaForm, existing *entity.EntitySchema, useJSON bool) {
	c := a.deps.Content
	var baseSchema, baseSettings json.RawMessage
	if existing != nil {
		baseSchema, baseSettings = existing.Schema, existing.Settings
	}
	settings, serrs := f.Settings.settingsJSON(baseSettings)
	for k, v := range serrs {
		f.Errors[k] = v
	}
	var schema json.RawMessage
	if useJSON {
		schema = json.RawMessage(strings.TrimSpace(f.JSON))
		if !json.Valid(schema) {
			f.Errors["schema_json"] = "This is not valid JSON"
			f.JSONErr = f.Errors["schema_json"]
		}
	} else {
		var ok bool
		if schema, ok = buildSchema(baseSchema, f.Fields); !ok {
			f.Errors["fields"] = "Fix the fields marked below"
		}
	}
	if len(f.Errors) > 0 {
		a.renderSchemaForm(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	var desc *string
	if f.Desc != "" {
		desc = &f.Desc
	}
	var saved *entity.EntitySchema
	var err error
	if existing == nil {
		saved, err = c.Entities.CreateSchema(r.Context(), &entity.CreateSchemaInput{Name: f.Name, Slug: f.Slug, Description: desc, Schema: schema, Settings: settings})
	} else {
		d := f.Desc
		saved, err = c.Entities.UpdateSchema(r.Context(), existing.ID, &entity.UpdateSchemaInput{Name: &f.Name, Description: &d, Schema: &schema, Settings: &settings})
	}
	if err != nil {
		if errors.Is(err, entity.ErrCannotModifyBuiltin) && existing != nil && existing.Slug == entity.PersonSchemaSlug {
			msg := sentence(strings.TrimPrefix(err.Error(), entity.ErrCannotModifyBuiltin.Error()+": "))
			if useJSON {
				f.Errors["schema_json"], f.JSONErr = msg, msg
			} else {
				f.Errors["fields"] = msg
			}
			a.renderSchemaForm(w, r, http.StatusUnprocessableEntity, f)
			return
		}
		if errors.Is(err, entity.ErrCannotModifyBuiltin) {
			a.Error(w, r, http.StatusForbidden, "Built-in type", "Built-in entity types cannot be changed.")
			return
		}
		if errors.Is(err, entity.ErrSchemaNotFound) {
			a.notFound(w, r, "Entity type", a.adminPath+"/settings/types", "Back to entity types")
			return
		}
		if schemaFormErr(f, err) {
			if useJSON {
				f.Fields = fieldRows(schema)
			}
			a.renderSchemaForm(w, r, http.StatusUnprocessableEntity, f)
			return
		}
		a.serverError(w, r, "The entity type could not be saved", err)
		return
	}
	if existing == nil {
		a.SetFlash(w, r, "success", "Entity type created.")
	} else {
		a.SetFlash(w, r, "success", "Entity type saved.")
	}
	Redirect(w, r, a.adminPath+"/settings/types/"+saved.ID.String())
}

func (a *Admin) handleSchemaDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	s, ok := a.loadSchema(w, r)
	if !ok {
		return
	}
	id := s.ID.String()
	if s.IsBuiltin {
		a.ErrorBack(w, r, http.StatusForbidden, "Built-in type", "Built-in entity types cannot be deleted.", a.adminPath+"/settings/types", "Back to entity types")
		return
	}
	a.renderConfirm(w, r, confirmData{Title: "Delete “" + s.Name + "”?", Message: "The entity type is deleted. This only works while no entities use it.",
		Post: a.adminPath + "/settings/types/" + id + "/delete", Label: "Delete entity type", Back: a.adminPath + "/settings/types/" + id, Danger: true})
}

func (a *Admin) handleSchemaDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := uuidParam(r, "id")
	if !ok || a.deps.Content.Entities == nil {
		a.notFound(w, r, "Entity type", a.adminPath+"/settings/types", "Back to entity types")
		return
	}
	err := a.deps.Content.Entities.DeleteSchema(r.Context(), id)
	switch {
	case err == nil:
		a.SetFlash(w, r, "success", "Entity type deleted.")
		Redirect(w, r, a.adminPath+"/settings/types")
	case errors.Is(err, entity.ErrSchemaNotFound):
		a.notFound(w, r, "Entity type", a.adminPath+"/settings/types", "Back to entity types")
	case errors.Is(err, entity.ErrCannotDeleteBuiltin):
		a.ErrorBack(w, r, http.StatusForbidden, "Built-in type", "Built-in entity types cannot be deleted.", a.adminPath+"/settings/types", "Back to entity types")
	case errors.Is(err, entity.ErrSchemaInUse):
		a.SetFlash(w, r, "error", "This entity type is still used by entities. Delete or move them first.")
		Redirect(w, r, a.adminPath+"/settings/types/"+id.String())
	default:
		a.serverError(w, r, "The entity type could not be deleted", err)
	}
}

var _ = uuid.Nil
