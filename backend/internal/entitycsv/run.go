package entitycsv

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/csvx"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// Source is the import source label stored in the entity metadata.
const Source = "csv"

// MaxListedErrors caps the row errors a Report lists; Failed keeps the count.
const MaxListedErrors = 200

// Importer is the part of entity.Service the import needs.
type Importer interface {
	Import(ctx context.Context, actor *uuid.UUID, batch entity.ImportBatch) (*entity.ImportResult, error)
}

// RowError is one row that cannot be imported. Row 1 is the header, so the
// first data row is 2, as a spreadsheet numbers it.
type RowError struct {
	Row     int    `json:"row"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}

// Report is the outcome of a dry or real run.
type Report struct {
	Rows    int        `json:"rows"`
	Created int        `json:"created"`
	Updated int        `json:"updated"`
	Failed  int        `json:"failed"`
	Errors  []RowError `json:"errors,omitempty"` // the first MaxListedErrors
}

func (r *Report) fail(e RowError) {
	r.Failed++
	if len(r.Errors) < MaxListedErrors {
		r.Errors = append(r.Errors, e)
	}
}

// Run reads the CSV and sends it to svc in batches of BatchSize. With dry set
// nothing is stored (entity.ImportBatch.DryRun). A row that cannot be parsed
// is reported and not sent; a row the service rejects is reported and the
// rest goes on.
func Run(ctx context.Context, svc Importer, actor *uuid.UUID, schema *entity.EntitySchema, m Mapping, r io.Reader, dry bool) (*Report, error) {
	cr := newReader(r)
	header, err := readHeader(cr)
	if err != nil {
		return nil, err
	}
	attrs := AttributeColumns(schema.Schema)
	if err := m.Validate(header, attrs); err != nil {
		return nil, err
	}
	target := make([]string, len(header)) // target per column index
	for i, h := range header {
		if t := m.Cols[h]; t != IgnoreTarget {
			target[i] = t
		}
	}
	status := m.Status
	if status == "" {
		status = entity.StatusDraft
	}

	rep := &Report{}
	ns := KeyNamespace(schema.Slug)
	var (
		items   []entity.ImportItem
		rowOf   []int
		keyRow  = map[string]int{}
		dryKeys = map[string]bool{} // keys an earlier dry batch "created"
	)
	flush := func() error {
		if len(items) == 0 {
			return nil
		}
		res, err := svc.Import(ctx, actor, entity.ImportBatch{Source: Source, Items: items, DryRun: dry})
		if err != nil {
			return err
		}
		for i, it := range res.Items {
			switch {
			case it.Err != nil:
				rep.fail(RowError{Row: rowOf[i], Message: it.Err.Error()})
			case it.Created:
				rep.Created++
				if dry {
					dryKeys[items[i].Key] = true
				}
			default:
				rep.Updated++
			}
		}
		items, rowOf = items[:0], rowOf[:0]
		return nil
	}

	for row := 2; ; row++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if blank(rec) {
			continue
		}
		rep.Rows++
		if rep.Rows > MaxRows {
			return nil, ErrTooManyRows
		}
		it, rowErr := buildItem(schema, target, header, rec, ns, status)
		if rowErr == nil {
			if first, dup := keyRow[it.Key]; dup {
				rowErr = &RowError{Column: keyColumn(target, header), Message: fmt.Sprintf("key %q is also on row %d", strings.TrimPrefix(it.Key, ns), first)}
			}
		}
		if rowErr != nil {
			rowErr.Row = row
			rep.fail(*rowErr)
			continue
		}
		keyRow[it.Key] = row
		if dry && it.ParentKey != "" && dryKeys[it.ParentKey] {
			// The parent of an earlier batch was rolled back; it passed its own checks.
			it.ParentKey = ""
		}
		items = append(items, *it)
		rowOf = append(rowOf, row)
		if len(items) == BatchSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return rep, nil
}

// idKey reads a key of the id:<uuid> form. isID is false for a plain key.
func idKey(v string) (id uuid.UUID, isID bool, err error) {
	raw, ok := strings.CutPrefix(v, IDKeyPrefix)
	if !ok {
		return uuid.Nil, false, nil
	}
	id, err = uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, true, fmt.Errorf("%q is not a key: after %q comes an id from an export", v, IDKeyPrefix)
	}
	return id, true, nil
}

func keyColumn(target, header []string) string {
	for i, t := range target {
		if t == ColKey {
			return header[i]
		}
	}
	return ""
}

// buildItem turns one record into an import item, or says why it cannot.
func buildItem(schema *entity.EntitySchema, target, header, rec []string, ns string, defStatus entity.EntityStatus) (*entity.ImportItem, *RowError) {
	it := &entity.ImportItem{SchemaSlug: schema.Slug}
	cells := map[string]string{}
	for i, t := range target {
		if t == "" || i >= len(rec) {
			continue
		}
		v := csvx.Unsafe(rec[i])
		bad := func(format string, a ...any) *RowError {
			return &RowError{Column: header[i], Message: fmt.Sprintf(format, a...)}
		}
		switch t {
		case ColKey:
			v = strings.TrimSpace(v)
			if v == "" {
				return nil, bad("the key is empty")
			}
			it.Key = ns + v
			if id, isID, err := idKey(v); isID {
				if err != nil {
					return nil, bad("%v", err)
				}
				it.TargetID = &id
			}
		case ColName:
			it.Name = v
		case ColSlug:
			it.Slug = strings.TrimSpace(v)
		case ColLocale:
			it.Locale = strings.TrimSpace(v)
		case ColParentKey:
			if v = strings.TrimSpace(v); v != "" {
				id, isID, err := idKey(v)
				switch {
				case err != nil:
					return nil, bad("%v", err)
				case isID:
					it.ParentID = &id
				default:
					it.ParentKey = ns + v
				}
			}
		case ColStatus:
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "" && !entity.ValidStatus(entity.EntityStatus(v)) {
				return nil, bad("%q is not a status (draft, scheduled, published or archived)", v)
			}
			it.Status = entity.EntityStatus(v)
		case ColPublishedAt:
			if v = strings.TrimSpace(v); v != "" {
				ts, err := parseDate(v)
				if err != nil {
					return nil, bad("%q is not a date (use 2006-01-02 or 2006-01-02T15:04:05Z)", v)
				}
				it.PublishedAt = ts
			}
		default:
			path, _ := attrPath(t)
			if v != "" {
				cells[path] = v
			}
		}
	}
	// A re-import by key changes what the file carries (an empty cell keeps the
	// stored value); the default status is for new rows only.
	it.Partial, it.NewStatus = true, defStatus
	attrs, whole, err := unflatten(schema.Schema, cells)
	if err != nil {
		var ce *CellError
		if errors.As(err, &ce) {
			return nil, &RowError{Column: attrHeader(target, header, ce.Path), Message: ce.Message}
		}
		return nil, &RowError{Message: err.Error()}
	}
	it.Attributes, it.ReplacePaths = attrs, whole
	return it, nil
}

func attrHeader(target, header []string, path string) string {
	for i, t := range target {
		if t == AttrPrefix+path {
			return header[i]
		}
	}
	return path
}

func parseDate(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("bad date")
}

// CellError is a cell that does not fit its attribute's type.
type CellError struct {
	Path    string
	Message string
}

func (e *CellError) Error() string { return e.Path + ": " + e.Message }

// Unflatten builds the attributes object from cells keyed by dotted path
// (a.b), typing each by the type's JSON schema: string, number, integer,
// boolean, an array of scalars (cells joined with ArraySep), or an object or
// other array given as JSON text. A path the schema does not know is a
// string. Empty cells are not in cells; they leave the attribute out.
func Unflatten(schema jsontext.Value, cells map[string]string) (jsontext.Value, error) {
	attrs, _, err := unflatten(schema, cells)
	return attrs, err
}

// unflatten is Unflatten that also returns the paths whose cell held a whole
// JSON object, which replace the stored object instead of merging into it.
func unflatten(schema jsontext.Value, cells map[string]string) (jsontext.Value, []string, error) {
	var whole []string
	root := map[string]any{}
	paths := make([]string, 0, len(cells))
	for p := range cells {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		val, err := typedCell(propertySchema(schema, p), p, cells[p])
		if err != nil {
			return nil, nil, err
		}
		if v, ok := val.(jsontext.Value); ok && v.Kind() == '{' {
			whole = append(whole, p)
		}
		parts := strings.Split(p, ".")
		cur := root
		for _, part := range parts[:len(parts)-1] {
			next, ok := cur[part]
			if !ok {
				next = map[string]any{}
				cur[part] = next
			}
			m, ok := next.(map[string]any)
			if !ok {
				return nil, nil, &CellError{p, "conflicts with the column that sets " + strings.Join(parts[:len(parts)-1], ".")}
			}
			cur = m
		}
		cur[parts[len(parts)-1]] = val
	}
	attrs, err := jsonx.Marshal(root)
	return attrs, whole, err
}

// propertySchema finds the schema of the property at a dotted path, or nil.
func propertySchema(schema jsontext.Value, path string) jsontext.Value {
	cur := schema
	for _, part := range strings.Split(path, ".") {
		var doc struct {
			Properties map[string]jsontext.Value `json:"properties"`
		}
		if len(cur) == 0 || jsonx.Unmarshal(cur, &doc, jsonx.StoredLoose) != nil {
			return nil
		}
		cur = doc.Properties[part]
	}
	return cur
}

// typedCell converts one cell by the property schema.
func typedCell(prop jsontext.Value, path, cell string) (any, error) {
	var p struct {
		Type  string         `json:"type"`
		Items jsontext.Value `json:"items"`
	}
	if len(prop) > 0 {
		_ = jsonx.Unmarshal(prop, &p, jsonx.StoredLoose)
	}
	if p.Type == "" && len(prop) > 0 && mayBeObject(prop) {
		// No single declared type (free-form, or a list such as ["object","null"]):
		// an object is exported as JSON text and comes back as the object.
		if v := jsontext.Value(strings.TrimSpace(cell)); v.IsValid() && v.Kind() == '{' {
			return v, nil
		}
	}
	switch p.Type {
	case "array":
		var it struct {
			Type string `json:"type"`
		}
		if len(p.Items) > 0 {
			_ = jsonx.Unmarshal(p.Items, &it, jsonx.StoredLoose)
		}
		switch it.Type {
		case "", "string", "number", "integer", "boolean":
			if strings.HasPrefix(strings.TrimSpace(cell), "[") && it.Type == "" {
				return jsonCell(path, cell, '[')
			}
			parts := strings.Split(cell, ArraySep)
			out := make([]any, len(parts))
			for i, part := range parts {
				v, err := scalarCell(it.Type, path, part)
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return out, nil
		}
		return jsonCell(path, cell, '[')
	case "object":
		return jsonCell(path, cell, '{')
	}
	return scalarCell(p.Type, path, cell)
}

// mayBeObject reports whether a property schema without a single type string
// lets the value be an object: it has no type, or its type list has "object".
func mayBeObject(prop jsontext.Value) bool {
	var p struct {
		Type jsontext.Value `json:"type"`
	}
	if len(prop) > 0 {
		_ = jsonx.Unmarshal(prop, &p, jsonx.StoredLoose)
	}
	if len(p.Type) == 0 {
		return true
	}
	var types []string
	return jsonx.Unmarshal(p.Type, &types, jsonx.StoredLoose) == nil && slices.Contains(types, "object")
}

// jsonCell reads a cell holding JSON text of the wanted kind.
func jsonCell(path, cell string, kind jsontext.Kind) (any, error) {
	v := jsontext.Value(strings.TrimSpace(cell))
	if !v.IsValid() || v.Kind() != kind {
		what := "a JSON object"
		if kind == '[' {
			what = "a JSON array"
		}
		return nil, &CellError{path, fmt.Sprintf("%q is not %s", cell, what)}
	}
	return v, nil
}

func scalarCell(typ, path, cell string) (any, error) {
	switch typ {
	case "number":
		text := strings.TrimSpace(cell)
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, &CellError{path, fmt.Sprintf("%q is not a number", cell)}
		}
		if v := jsontext.Value(text); v.IsValid() && v.Kind() == '0' {
			return v, nil
		}
		return jsontext.Value(strconv.FormatFloat(f, 'f', -1, 64)), nil
	case "integer":
		text := strings.TrimSpace(cell)
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, &CellError{path, fmt.Sprintf("%q is not a whole number", cell)}
		}
		return n, nil
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(cell)) {
		case "true", "yes", "1":
			return true, nil
		case "false", "no", "0":
			return false, nil
		}
		return nil, &CellError{path, fmt.Sprintf("%q is not true or false", cell)}
	}
	return cell, nil
}
