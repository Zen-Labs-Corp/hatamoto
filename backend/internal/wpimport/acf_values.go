package wpimport

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v5"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// acfWarnCap is how many value warnings one run records; the rest are summed up.
const acfWarnCap = 200

// acfTextMax is the longest text value of a nested repeater or flexible
// content field kept as text (the maxLength of its property).
const acfTextMax = 65535

// acfReader converts the ACF meta values of one item into attribute values.
// The meta comes from the source site and is untrusted: every conversion
// failure leaves the value out or keeps it as text, and nothing panics.
type acfReader struct {
	r    *Runner
	ctx  context.Context
	item *Item
	// withEntities is true in the link pass, where internal links resolve.
	withEntities bool
	// quiet drops the warnings: the content step builds relation-bearing
	// fields only to match the links step, which reports them.
	quiet bool
	loc   *time.Location
	// unresolved counts related items without an entity; dropped counts
	// media ids that were not imported.
	unresolved, dropped int
	// rewritten and left are the media URL stats of rich text values.
	rewritten int
	left      []string
	// links counts the relation ids kept, rows the repeater rows and flexible
	// content rows rebuilt.
	links, rows int
	// rowProps are the stored properties of the row fields of the field being
	// read; their enum and maxLength win over the ACF definition.
	rowProps map[string]any
}

func (r *Runner) newACFReader(ctx context.Context, it *Item, withEntities bool) *acfReader {
	loc := time.UTC
	if tz := r.dump.Site.Timezone; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	return &acfReader{r: r, ctx: ctx, item: it, withEntities: withEntities, loc: loc}
}

// acfQuoteMax is how many runes of a source value a warning quotes.
const acfQuoteMax = 80

// quoted shortens a source value for a warning.
func quoted(s string) string {
	if utf8.RuneCountInString(s) <= acfQuoteMax {
		return s
	}
	return string([]rune(s)[:acfQuoteMax]) + "…"
}

// warn records a warning about this item, up to the cap. Text arguments come
// from the source site and are shortened.
func (rd *acfReader) warn(format string, a ...any) {
	if rd.quiet {
		return
	}
	for i, v := range a {
		if s, ok := v.(string); ok {
			a[i] = quoted(s)
		}
	}
	r := rd.r
	switch {
	case r.acfWarns < acfWarnCap:
		r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("%s %s: %s", rd.item.Type, rd.item.ID, fmt.Sprintf(format, a...)))
	case r.acfWarns == acfWarnCap:
		r.res.Warnings = append(r.res.Warnings, "more ACF value warnings were left out")
	}
	r.acfWarns++
}

// acfRelationBearing reports whether a top-level field holds related items,
// directly or inside its rows: those values wait for the links step.
func acfRelationBearing(m acfMapped) bool {
	switch m.Kind {
	case acfKindRelation:
		return true
	case acfKindRepeater, acfKindGroup:
		return subsRelate(m.Field.Sub)
	case acfKindFlex:
		for _, l := range m.Field.Layouts {
			if subsRelate(l.Sub) {
				return true
			}
		}
	}
	return false
}

// acfHasRichtext reports whether a relation-bearing field holds rich text in
// its rows. Its links resolve only in the links step, once every item has an
// entity and a path.
func acfHasRichtext(m acfMapped) bool {
	var has func(subs []ACFField) bool
	has = func(subs []ACFField) bool {
		for _, s := range subs {
			if _, k, _, _ := acfNode(s, true); k == acfKindRichtext || has(s.Sub) {
				return true
			}
		}
		return false
	}
	if has(m.Field.Sub) {
		return true
	}
	for _, l := range m.Field.Layouts {
		if has(l.Sub) {
			return true
		}
	}
	return false
}

// carryStored replaces the value the content step built for prop with the one
// the item's entity holds, which the links step wrote on an earlier run. The
// content step cannot resolve the links in such a value, so writing its own
// would undo the links step on every run. A new item has no stored value.
func (rd *acfReader) carryStored(prop string, out map[string]any) {
	if _, ok := out[prop]; !ok {
		return
	}
	r := rd.r
	id, ok, err := r.st.Lookup(rd.ctx, KindEntity, rd.item.ID)
	if err != nil || !ok {
		return
	}
	ent, err := r.d.Entities.GetEntity(rd.ctx, id)
	if err != nil {
		return
	}
	var stored map[string]any
	if jsonx.Unmarshal(ent.Attributes, &stored, jsonx.StoredLoose) != nil {
		return
	}
	if v, ok := stored[prop]; ok {
		out[prop] = v
	}
}

func subsRelate(subs []ACFField) bool {
	for _, s := range subs {
		if _, k, _, _ := acfNode(s, true); k == acfKindRelation {
			return true
		}
	}
	return false
}

// acfAttrs builds the ACF attributes of one item from its meta and the fields
// mapped for its type. A field with related items is built the way the links
// step builds it (unresolved ids dropped, links resolved), so the content
// write of a re-run carries the value the links step would write again.
func (r *Runner) acfAttrs(ctx context.Context, it *Item, withEntities bool) map[string]any {
	fields := r.acf[it.Type]
	if len(fields) == 0 || len(it.Meta) == 0 {
		return nil
	}
	out := map[string]any{}
	rd := r.newACFReader(ctx, it, withEntities)
	rel := rd
	if !withEntities {
		rel = r.newACFReader(ctx, it, true)
		rel.quiet = true
	}
	for _, m := range fields {
		if acfRelationBearing(m) {
			rel.field(m, out)
			if !withEntities && acfHasRichtext(m) {
				rd.carryStored(m.Prop, out)
			}
			continue
		}
		rd.field(m, out)
		if !withEntities {
			rd.count(m, out)
		}
	}
	if !withEntities {
		r.res.MediaRewritten += rd.rewritten + rel.rewritten
		r.res.Unresolved = append(r.res.Unresolved, append(rd.left, rel.left...)...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// field writes the value of one top-level field (and its _raw fallback) into out.
func (rd *acfReader) field(m acfMapped, out map[string]any) {
	if m.Skipped || m.Kind == acfKindNone {
		return
	}
	rd.unresolved, rd.dropped, rd.rows, rd.links = 0, 0, 0, 0
	rd.rowProps = storedRowProps(m.Stored, m.Kind)
	val, raw, ok := rd.value(m.Field, m.Kind, m.Node, rd.item.Meta, m.Field.Name)
	if raw != "" {
		rd.keepRaw(out, m.Prop, m.Field, raw)
		return
	}
	if ok {
		out[m.Prop] = val
	}
}

// keepRaw stores a value that could not be converted under <prop>_raw.
func (rd *acfReader) keepRaw(out map[string]any, prop string, f ACFField, raw string) {
	if IsWPContent(raw) {
		rd.warn("ACF %s: value left out, it points into the old site", firstNonEmpty(f.Label, f.Name))
		return
	}
	out[prop+"_raw"] = raw
	rd.warn("ACF %s: kept as %s_raw", firstNonEmpty(f.Label, f.Name), prop)
}

// count adds the item to the report line of a field that was written.
func (rd *acfReader) count(m acfMapped, out map[string]any) {
	if _, ok := out[m.Prop]; !ok {
		return
	}
	if i := rd.r.acfLine(m, rd.item.Type); i != nil {
		i.Items++
		i.Rows += rd.rows
	}
	rd.rows = 0
}

// acfLine finds the report line of a mapped field.
func (r *Runner) acfLine(m acfMapped, typ string) *ACFFieldResult {
	if r.res.ACF == nil {
		return nil
	}
	label := firstNonEmpty(m.Field.Label, m.Field.Name)
	for i := range r.res.ACF.Fields {
		l := &r.res.ACF.Fields[i]
		if l.Group == m.Group.Title && l.Label == label && l.Target == r.schemaOf[typ] && l.Type == m.Field.Type {
			return l
		}
	}
	return nil
}

// value converts the meta value of one field: val is the attribute value,
// raw is the source text when the conversion failed, and ok is false when
// there is nothing to write.
func (rd *acfReader) value(f ACFField, kind acfKind, node map[string]any, meta map[string]string, key string) (val any, raw string, ok bool) {
	v := meta[key]
	switch kind {
	case acfKindRepeater:
		return rd.repeater(f, meta, key)
	case acfKindGroup:
		return rd.group(f, meta, key)
	case acfKindFlex:
		return rd.flex(f, meta, key)
	case acfKindRowText:
		t := rd.rowText(f, meta, key, 0)
		return t, "", t != ""
	}
	if v == "" {
		return nil, "", false
	}
	switch kind {
	case acfKindText, acfKindTime:
		return rd.text(v, node)
	case acfKindRichtext:
		html, st := rd.r.bodyHTML(rd.ctx, v, rd.withEntities)
		rd.rewritten += st.MediaRewritten
		rd.left = append(rd.left, st.Unresolved...)
		return html, "", html != ""
	case acfKindNumber:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, v, false
		}
		return n, "", true
	case acfKindBool:
		switch strings.TrimSpace(v) {
		case "1", "true":
			return true, "", true
		case "0", "false":
			return false, "", true
		}
		return nil, v, false
	case acfKindChoice:
		if strings.HasPrefix(v, "a:") {
			if l := phpStrings(v); len(l) > 0 {
				v = l[0]
			}
		}
		if enum, _ := node["enum"].([]string); len(enum) > 0 && !hasString(enum, v) {
			return nil, v, false
		}
		return v, "", true
	case acfKindMulti:
		l := phpStrings(v)
		return l, "", len(l) > 0
	case acfKindDate:
		t, err := time.Parse("20060102", strings.TrimSpace(v))
		if err != nil {
			return nil, v, false
		}
		return t.Format("2006-01-02"), "", true
	case acfKindDateTime:
		t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(v), rd.loc)
		if err != nil {
			return nil, v, false
		}
		return t.Format(time.RFC3339), "", true
	case acfKindLink:
		return rd.link(v)
	case acfKindMedia:
		if u := rd.mediaURL(strings.TrimSpace(v)); u != "" {
			return u, "", true
		}
		rd.dropped++
		rd.warn("ACF %s: file %s was not imported and is left out", firstNonEmpty(f.Label, f.Name), strings.TrimSpace(v))
		return nil, "", false
	case acfKindGallery:
		var urls []string
		for _, id := range phpStrings(v) {
			if u := rd.mediaURL(id); u != "" {
				urls = append(urls, u)
			} else {
				rd.dropped++
			}
		}
		if rd.dropped > 0 {
			rd.warn("ACF %s: %d images were not imported and are left out", firstNonEmpty(f.Label, f.Name), rd.dropped)
		}
		return urls, "", len(urls) > 0
	case acfKindRelation:
		ids := phpStrings(v)
		if len(ids) == 0 {
			return nil, "", false
		}
		out := []string{}
		for _, id := range ids {
			if u, found := rd.r.relatedID(rd.ctx, id); found {
				out = append(out, u)
			} else {
				rd.unresolved++
			}
		}
		rd.links += len(out)
		if rd.unresolved > 0 {
			rd.warn("ACF %s: %d related items were not imported and are left out", firstNonEmpty(f.Label, f.Name), rd.unresolved)
		}
		return out, "", true
	}
	return v, "", true
}

// mediaURL is the imported public URL of an attachment id, "" when it was not imported.
func (rd *acfReader) mediaURL(id string) string {
	if m, ok := rd.r.media.byID[id]; ok && m.PublicURL != nil {
		return *m.PublicURL
	}
	return ""
}

// text checks a plain text value against what its property allows.
func (rd *acfReader) text(v string, node map[string]any) (any, string, bool) {
	switch node["format"] {
	case "email":
		if !jsonschema.Formats["email"](strings.TrimSpace(v)) {
			return nil, v, false
		}
		return strings.TrimSpace(v), "", true
	case "uri":
		v = strings.TrimSpace(v)
		if IsWPContent(v) {
			u := rd.r.mediaURL(rd.ctx, v)
			if u == "" {
				return nil, "", false
			}
			return u, "", true
		}
		if !acfLooksLikeURI(v) {
			return nil, v, false
		}
		return v, "", true
	}
	if n, _ := node["maxLength"].(int); n > 0 && len(v) > n {
		v = truncateUTF8(v, n)
		rd.warn("a long ACF text value was cut to %d bytes", n)
	}
	if IsWPContent(v) {
		return nil, v, false
	}
	return v, "", true
}

// acfLooksLikeURI is the check the entity validator makes for format "uri",
// minus the schemes that run code when followed.
func acfLooksLikeURI(v string) bool {
	if !entity.IsValidURI(v) {
		return false
	}
	scheme, _, _ := strings.Cut(v, ":")
	switch strings.ToLower(scheme) {
	case "javascript", "data", "vbscript":
		return false
	}
	return true
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// link reads the url of an ACF link array (or a plain url).
func (rd *acfReader) link(v string) (any, string, bool) {
	u := strings.TrimSpace(v)
	if strings.HasPrefix(v, "a:") {
		parsed, err := phpUnserialize(v)
		if err != nil {
			return nil, v, false
		}
		u = strings.TrimSpace(phpText(parsed, "url"))
	}
	if u == "" {
		return nil, "", false
	}
	if IsWPContent(u) {
		if got := rd.r.mediaURL(rd.ctx, u); got != "" {
			return got, "", true
		}
		return nil, "", false
	}
	if !acfLooksLikeURI(u) {
		return nil, v, false
	}
	return u, "", true
}

// storedRowProps are the properties of the rows of a stored repeater, group or
// flexible content property; nil for any other kind or without a stored one.
func storedRowProps(stored map[string]any, kind acfKind) map[string]any {
	obj := stored
	if kind == acfKindRepeater || kind == acfKindFlex {
		obj, _ = stored["items"].(map[string]any)
	} else if kind != acfKindGroup {
		return nil
	}
	props, _ := obj["properties"].(map[string]any)
	return props
}

// repeater rebuilds the rows of a repeater field from <key>_<i>_<sub>.
func (rd *acfReader) repeater(f ACFField, meta map[string]string, key string) (any, string, bool) {
	n := rd.rowCount(f, meta[key])
	if n == 0 {
		return nil, "", false
	}
	names := subNames(f.Sub)
	rows := make([]any, 0, n)
	for i := 0; i < n; i++ {
		prefix := key + "_" + strconv.Itoa(i) + "_"
		row := map[string]any{}
		for si, s := range f.Sub {
			rd.subValue(row, names[si], s, meta, prefix+s.Name)
		}
		rows = append(rows, row)
	}
	rd.rows += len(rows)
	return rows, "", true
}

// rowCount reads the stored row count, bounded.
func (rd *acfReader) rowCount(f ACFField, v string) int {
	n := atoi(strings.TrimSpace(v))
	if n < 0 {
		return 0
	}
	if n > acfMaxRows {
		rd.warn("ACF %s: only the first %d of %d rows are kept", firstNonEmpty(f.Label, f.Name), acfMaxRows, n)
		return acfMaxRows
	}
	return n
}

// group reads a group field: one object of its sub fields.
func (rd *acfReader) group(f ACFField, meta map[string]string, key string) (any, string, bool) {
	names := subNames(f.Sub)
	row := map[string]any{}
	for si, s := range f.Sub {
		rd.subValue(row, names[si], s, meta, key+"_"+s.Name)
	}
	return row, "", len(row) > 0
}

// flex rebuilds flexible content rows: each row carries its layout name.
func (rd *acfReader) flex(f ACFField, meta map[string]string, key string) (any, string, bool) {
	stored := phpStrings(meta[key])
	if len(stored) == 0 {
		return nil, "", false
	}
	if len(stored) > acfMaxRows {
		rd.warn("ACF %s: only the first %d of %d rows are kept", firstNonEmpty(f.Label, f.Name), acfMaxRows, len(stored))
		stored = stored[:acfMaxRows]
	}
	names := layoutNames(f.Layouts)
	rows := make([]any, 0, len(stored))
	for i, name := range stored {
		li := -1
		for j, l := range f.Layouts {
			if l.Name == name || l.Key == name {
				li = j
				break
			}
		}
		if li < 0 {
			rd.warn("ACF %s: row %d has the unknown layout %q and is left out", firstNonEmpty(f.Label, f.Name), i+1, name)
			continue
		}
		prefix := key + "_" + strconv.Itoa(i) + "_"
		layout := layoutLabel(f.Layouts[li])
		if lp, ok := rd.rowProps["layout"].(map[string]any); ok {
			if enum, ok := lp["enum"].([]any); ok && !slices.Contains(enum, any(layout)) {
				rd.warn("ACF %s: row %d has the layout %q, which the stored property does not list, and is left out", firstNonEmpty(f.Label, f.Name), i+1, layout)
				continue
			}
		}
		row := map[string]any{"layout": layout}
		for si, s := range f.Layouts[li].Sub {
			rd.subValue(row, names[li][si], s, meta, prefix+s.Name)
		}
		rows = append(rows, row)
	}
	rd.rows += len(rows)
	return rows, "", len(rows) > 0
}

// subValue writes one sub field of a row; a failed conversion is kept as <name>_raw.
func (rd *acfReader) subValue(row map[string]any, name string, s ACFField, meta map[string]string, key string) {
	node, kind, _, _ := acfNode(s, true)
	if node == nil {
		return
	}
	if sp, ok := rd.rowProps[name].(map[string]any); ok {
		node = followStored(node, sp)
	}
	val, raw, ok := rd.value(s, kind, node, meta, key)
	switch {
	case raw != "":
		rd.keepRaw(row, name, s, raw)
	case ok:
		row[name] = val
	}
}

// rowText flattens a nested repeater, group or flexible content field into
// "name: value" lines; the nesting itself is not carried (the field is lossy).
func (rd *acfReader) rowText(f ACFField, meta map[string]string, key string, depth int) string {
	if depth >= acfMaxDepth {
		return ""
	}
	var lines []string
	add := func(s ACFField, k string) {
		switch s.Type {
		case "repeater", "group", "flexible_content":
			if t := rd.rowText(s, meta, k, depth+1); t != "" {
				lines = append(lines, s.Name+":\n"+t)
			}
		default:
			if v := meta[k]; v != "" {
				lines = append(lines, s.Name+": "+strings.Join(phpStrings(v), ", "))
			}
		}
	}
	switch f.Type {
	case "group":
		for _, s := range f.Sub {
			add(s, key+"_"+s.Name)
		}
	case "flexible_content":
		for i, name := range phpStrings(meta[key]) {
			if i >= acfMaxRows {
				break
			}
			for _, l := range f.Layouts {
				if l.Name == name || l.Key == name {
					lines = append(lines, name)
					for _, s := range l.Sub {
						add(s, key+"_"+strconv.Itoa(i)+"_"+s.Name)
					}
				}
			}
		}
	default:
		n := min(atoi(strings.TrimSpace(meta[key])), acfMaxRows)
		for i := 0; i < n; i++ {
			for _, s := range f.Sub {
				add(s, key+"_"+strconv.Itoa(i)+"_"+s.Name)
			}
		}
	}
	t := strings.Join(lines, "\n")
	if IsWPContent(t) {
		return ""
	}
	return truncateUTF8(t, acfTextMax)
}

// relatedID resolves a source post id to the id of its imported entity.
func (r *Runner) relatedID(ctx context.Context, id string) (string, bool) {
	if info, ok := r.infos[id]; ok && info.ent != nil {
		return info.ent.ID.String(), true
	}
	if r.relIDs == nil {
		r.relIDs = map[string]uuid.UUID{}
		all, err := r.st.All(ctx, KindEntity)
		if err != nil {
			r.res.Warnings = append(r.res.Warnings, fmt.Sprintf("ACF related items: the id map could not be read: %v", err))
		}
		for k, v := range all {
			r.relIDs[k] = v
		}
	}
	if u, ok := r.relIDs[id]; ok {
		return u.String(), true
	}
	return "", false
}
