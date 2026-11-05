package entitycsv

import (
	"bytes"
	"encoding/csv"
	"encoding/json/jsontext"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/csvx"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// ArraySep joins the elements of a scalar array in one cell.
const ArraySep = "|"

// KeyNamespace is the import key prefix of CSV rows of one type: csv:<type>:.
func KeyNamespace(typeSlug string) string { return "csv:" + typeSlug + ":" }

// ExportOptions tune WriteCSV.
type ExportOptions struct {
	// Type is the slug of the exported type; it names the key namespace.
	Type string
	// ParentKey returns the CSV key of the parent entity (without namespace),
	// or "" when it has none. Nil leaves the parent_key column empty.
	ParentKey func(id uuid.UUID) string
}

// WriteCSV writes a BOM, the header (the column ids) and one row per entity
// that next returns, until next returns no rows. Every cell goes through
// csvx.Safe. The writer is flushed after each page.
func WriteCSV(w io.Writer, cols []Column, next func() ([]*entity.Entity, error), opt ExportOptions) error {
	if _, err := io.WriteString(w, csvx.BOM); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.ID
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for {
		page, err := next()
		if err != nil {
			cw.Flush()
			return err
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if err := cw.Write(row(cols, e, opt)); err != nil {
				return err
			}
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func row(cols []Column, e *entity.Entity, opt ExportOptions) []string {
	var flat map[string]string
	out := make([]string, len(cols))
	for i, c := range cols {
		var cell string
		if path, ok := attrPath(c.ID); ok {
			if flat == nil {
				flat = Flatten(e.Attributes)
			}
			cell = flat[path]
		} else {
			cell = fixedCell(c.ID, e, opt)
		}
		out[i] = csvx.Safe(cell)
	}
	return out
}

func fixedCell(id string, e *entity.Entity, opt ExportOptions) string {
	switch id {
	case ColKey:
		return KeyOf(e.Metadata, opt.Type)
	case ColName:
		return e.Name
	case ColSlug:
		return e.Slug
	case ColStatus:
		return string(e.Status)
	case ColPublishedAt:
		if e.PublishedAt == nil {
			return ""
		}
		return e.PublishedAt.UTC().Format(time.RFC3339)
	case ColLocale:
		return e.Locale
	case ColParentKey:
		if e.ParentID == nil || opt.ParentKey == nil {
			return ""
		}
		return opt.ParentKey(*e.ParentID)
	case ColOrder:
		return strconv.Itoa(e.SortOrder)
	case ColID:
		return e.ID.String()
	}
	return ""
}

// KeyOf returns the CSV key of an entity's metadata without the csv:<type>:
// namespace, or "" when the row was not created by a CSV import of that type.
func KeyOf(metadata jsontext.Value, typeSlug string) string {
	if len(metadata) == 0 {
		return ""
	}
	var m struct {
		ImportKey string `json:"import_key"`
	}
	if jsonx.Unmarshal(metadata, &m, jsonx.StoredLoose) != nil {
		return ""
	}
	key, _ := strings.CutPrefix(m.ImportKey, KeyNamespace(typeSlug))
	if key == m.ImportKey {
		return ""
	}
	return key
}

// Flatten turns an attributes object into one cell per leaf, keyed by the
// dotted path (a.b). A scalar array becomes its elements joined with ArraySep;
// any other array, and the top-level page_doc, becomes JSON text. null and
// empty objects give no cell.
func Flatten(attrs jsontext.Value) map[string]string {
	out := map[string]string{}
	if len(attrs) > 0 {
		flattenInto(out, "", attrs)
	}
	return out
}

func flattenInto(out map[string]string, prefix string, raw jsontext.Value) {
	switch raw.Kind() {
	case '{':
		entries, err := jsonx.ObjectEntries(raw)
		if err != nil {
			return
		}
		for _, e := range entries {
			path := prefix + e.Key
			if prefix == "" && e.Key == PageDocAttr {
				out[path] = compact(e.Value)
				continue
			}
			flattenInto(out, path+".", e.Value)
		}
	case '[':
		out[strings.TrimSuffix(prefix, ".")] = arrayCell(raw)
	case 'n':
	default:
		out[strings.TrimSuffix(prefix, ".")] = scalarText(raw)
	}
}

// arrayCell joins a scalar array with ArraySep; anything else is JSON text.
func arrayCell(raw jsontext.Value) string {
	var items []jsontext.Value
	if jsonx.Unmarshal(raw, &items, jsonx.StoredLoose) != nil {
		return compact(raw)
	}
	parts := make([]string, len(items))
	for i, it := range items {
		switch it.Kind() {
		case '"', '0', 't', 'f':
			parts[i] = scalarText(it)
		default:
			return compact(raw)
		}
	}
	return strings.Join(parts, ArraySep)
}

func scalarText(raw jsontext.Value) string {
	if raw.Kind() == '"' {
		var s string
		if jsonx.Unmarshal(raw, &s, jsonx.StoredLoose) == nil {
			return s
		}
	}
	return string(raw)
}

func compact(raw jsontext.Value) string {
	v := bytes.Clone(raw)
	j := jsontext.Value(v)
	if err := j.Compact(); err != nil {
		return string(raw)
	}
	return string(j)
}
