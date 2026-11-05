package entity

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"

	"hatamoto.dev/core/internal/jsonx"
)

// kv is one member of a JSON object, kept in document order.
type kv struct {
	key string
	val jsontext.Value
}

// orderedObject is a JSON object that round-trips with its key order intact
// (encoding/json maps would sort keys). Values stay raw, so anything not
// touched is re-emitted byte-for-byte.
type orderedObject []kv

func (o *orderedObject) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	if tok, err := dec.ReadToken(); err != nil { // '{'
		return err
	} else if tok.Kind() != '{' {
		return errors.New("entity: not a JSON object")
	}
	var out orderedObject
	for dec.PeekKind() != '}' {
		k, err := dec.ReadToken()
		if err != nil {
			return err
		}
		key := k.String() // only valid until the next read
		v, err := dec.ReadValue()
		if err != nil {
			return err
		}
		out = append(out, kv{key, v.Clone()})
	}
	if _, err := dec.ReadToken(); err != nil { // '}'
		return err
	}
	*o = out
	return nil
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := marshalNoEscape(m.key)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(m.val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (o orderedObject) index(key string) int {
	for i, m := range o {
		if m.key == key {
			return i
		}
	}
	return -1
}

func marshalNoEscape(v any) ([]byte, error) {
	return jsonx.Marshal(v)
}

// SanitizeHTMLSections rewrites attributes.page_sections[type=html or an HTML_SECTION_TYPE_ALIASES type].content.body
// to its sanitised (canonical) form. It returns the new attributes and the
// number of sections whose body changed. When nothing changed the input is
// returned verbatim. Only changed sections (and the containers on the path to
// them) are re-marshalled, with key order preserved and every other value
// byte-identical. (Postgres jsonb re-orders keys on storage regardless.)
// Malformed or unexpected shapes are left alone.
func SanitizeHTMLSections(raw jsontext.Value) (jsontext.Value, int, error) {
	var root orderedObject
	if err := jsonx.Unmarshal(raw, &root, jsonx.StoredLoose); err != nil {
		return raw, 0, nil
	}
	pi := root.index("page_sections")
	if pi < 0 {
		return raw, 0, nil
	}
	var sections []jsontext.Value
	if err := jsonx.Unmarshal(root[pi].val, &sections, jsonx.StoredLoose); err != nil {
		return raw, 0, nil
	}
	changed := 0
	for i, rawSec := range sections {
		var sec orderedObject
		if jsonx.Unmarshal(rawSec, &sec, jsonx.StoredLoose) != nil {
			continue
		}
		ti, ci := sec.index("type"), sec.index("content")
		if ti < 0 || ci < 0 {
			continue
		}
		var typ string
		if jsonx.Unmarshal(sec[ti].val, &typ, jsonx.StoredLoose) != nil || !isHTMLSectionType(typ) {
			continue
		}
		var content orderedObject
		if jsonx.Unmarshal(sec[ci].val, &content, jsonx.StoredLoose) != nil {
			continue
		}
		bi := content.index("body")
		if bi < 0 {
			continue
		}
		var body string
		if jsonx.Unmarshal(content[bi].val, &body, jsonx.StoredLoose) != nil {
			continue
		}
		clean := sanitizeHTMLBody(body)
		if clean == body {
			continue
		}
		nb, err := marshalNoEscape(clean)
		if err != nil {
			return raw, 0, err
		}
		content[bi].val = nb
		cb, err := content.MarshalJSON()
		if err != nil {
			return raw, 0, err
		}
		sec[ci].val = cb
		sb, err := sec.MarshalJSON()
		if err != nil {
			return raw, 0, err
		}
		sections[i] = sb
		changed++
	}
	if changed == 0 {
		return raw, 0, nil
	}
	// Join by hand: a plain marshal would compact and HTML-escape every element,
	// including the untouched ones.
	arr := []byte{'['}
	for i, s := range sections {
		if i > 0 {
			arr = append(arr, ',')
		}
		arr = append(arr, s...)
	}
	root[pi].val = append(arr, ']')
	out, err := root.MarshalJSON()
	if err != nil {
		return raw, 0, err
	}
	return out, changed, nil
}
