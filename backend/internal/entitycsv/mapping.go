package entitycsv

import (
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

// IgnoreTarget leaves a CSV column out of the import.
const IgnoreTarget = "ignore"

// Mapping says what each CSV column means for one content type.
type Mapping struct {
	// Type is the slug of the content type the rows become.
	Type string `json:"type"`
	// Cols maps a header cell to a target: key, name, slug, status,
	// published_at, locale, parent_key, attr:<path> or ignore.
	Cols map[string]string `json:"cols"`
	// Status is the status of rows without a mapped Status column (or with an
	// empty cell); draft when empty.
	Status entity.EntityStatus `json:"status,omitempty"`
}

// importable lists the fixed targets an import can set.
var importable = map[string]bool{
	ColKey: true, ColName: true, ColSlug: true, ColStatus: true,
	ColPublishedAt: true, ColLocale: true, ColParentKey: true,
}

// Target is one choice of the mapping step.
type Target struct {
	ID    string
	Label string
}

// Targets lists what a CSV column can be mapped to for a type, "ignore" last.
func Targets(attrs []Column) []Target {
	var out []Target
	for _, c := range FixedColumns {
		if importable[c.ID] {
			out = append(out, Target{c.ID, c.Label})
		}
	}
	for _, c := range attrs {
		out = append(out, Target{c.ID, "Attribute: " + strings.TrimPrefix(c.ID, AttrPrefix)})
	}
	return append(out, Target{IgnoreTarget, "Ignore this column"})
}

// Validate checks the mapping against the CSV header and the type's
// attribute columns: key and name are mapped, every target is used at most
// once, every target exists and every mapped column is in the header.
func (m *Mapping) Validate(header []string, attrs []Column) error {
	inHeader := map[string]bool{}
	for _, h := range header {
		inHeader[h] = true
	}
	attr := map[string]bool{}
	for _, c := range attrs {
		attr[c.ID] = true
	}
	used := map[string]string{}
	for col, target := range m.Cols {
		if !inHeader[col] {
			return fmt.Errorf("column %q is not in the file", col)
		}
		if target == IgnoreTarget || target == "" {
			continue
		}
		if !importable[target] && !attr[target] {
			return fmt.Errorf("column %q: %q is not something this type can import", col, target)
		}
		if other, dup := used[target]; dup {
			return fmt.Errorf("columns %q and %q are both mapped to %s; map each target once", other, col, targetLabel(target))
		}
		used[target] = col
	}
	if used[ColKey] == "" {
		return fmt.Errorf("map one column to Key: it is how a second import finds the rows it already created")
	}
	if used[ColName] == "" {
		return fmt.Errorf("map one column to Name")
	}
	if m.Status != "" && !entity.ValidStatus(m.Status) {
		return fmt.Errorf("%q is not a status", m.Status)
	}
	return nil
}

func targetLabel(t string) string {
	for _, c := range FixedColumns {
		if c.ID == t {
			return c.Label
		}
	}
	return strings.TrimPrefix(t, AttrPrefix)
}

// Suggest proposes a target for each header cell by its name: a fixed column
// id or label, an attr:<path> id, or a bare attribute path. Anything else is
// ignored; a target is proposed once (the first column wins).
func Suggest(header []string, attrs []Column) map[string]string {
	byName := map[string]string{}
	for _, c := range FixedColumns {
		if importable[c.ID] {
			byName[strings.ToLower(c.ID)] = c.ID
			byName[strings.ToLower(strings.SplitN(c.Label, " (", 2)[0])] = c.ID
		}
	}
	for _, c := range attrs {
		path := strings.TrimPrefix(c.ID, AttrPrefix)
		byName[strings.ToLower(c.ID)] = c.ID
		if _, taken := byName[strings.ToLower(path)]; !taken {
			byName[strings.ToLower(path)] = c.ID
		}
	}
	out := make(map[string]string, len(header))
	used := map[string]bool{}
	for _, h := range header {
		t, ok := byName[strings.ToLower(strings.TrimSpace(h))]
		if !ok || used[t] {
			out[h] = IgnoreTarget
			continue
		}
		used[t] = true
		out[h] = t
	}
	return out
}
