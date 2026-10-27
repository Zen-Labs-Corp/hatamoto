package schemaform

import (
	"bytes"
	"encoding/json"
	"slices"
)

// WithoutProperties returns schema with the named top-level properties (and
// their entries in "required") removed, keeping the order of the rest. A form
// that renders such a property elsewhere (the SEO panel) uses it so the field
// is not shown twice; values of removed properties still round-trip through
// Decode's prev map.
func WithoutProperties(schema json.RawMessage, names ...string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(schema)) == 0 {
		return schema, nil
	}
	keys, m, err := objectEntries(schema)
	if err != nil {
		return nil, err
	}
	if raw, ok := m["properties"]; ok {
		pk, pm, err := objectEntries(raw)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		b.WriteByte('{')
		first := true
		for _, k := range pk {
			if slices.Contains(names, k) {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteByte(':')
			b.Write(pm[k])
		}
		b.WriteByte('}')
		m["properties"] = b.Bytes()
	}
	if raw, ok := m["required"]; ok {
		var req []string
		if json.Unmarshal(raw, &req) == nil {
			req = slices.DeleteFunc(req, func(s string) bool { return slices.Contains(names, s) })
			if len(req) == 0 {
				delete(m, "required")
			} else {
				m["required"], _ = json.Marshal(req)
			}
		}
	}
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
