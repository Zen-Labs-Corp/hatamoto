package entity

import (
	"bytes"
	"encoding/json"
)

// kv is one member of a JSON object, kept in document order.
type kv struct {
	key string
	val json.RawMessage
}

// orderedObject is a JSON object that round-trips with its key order intact
// (encoding/json maps would sort keys). Values stay raw, so anything not
// touched is re-emitted byte-for-byte.
type orderedObject []kv

func (o *orderedObject) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil { // '{'
		return err
	}
	var out orderedObject
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		out = append(out, kv{k.(string), v})
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
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SanitizeHTMLSections rewrites attributes.page_sections[type=html or an HTML_SECTION_TYPE_ALIASES type].content.body
// to its sanitised (canonical) form. It returns the new attributes and the
// number of sections whose body changed. When nothing changed the input is
// returned verbatim. Only changed sections (and the containers on the path to
// them) are re-marshalled, with key order preserved and every other value
// byte-identical. (Postgres jsonb re-orders keys on storage regardless.)
// Malformed or unexpected shapes are left alone.
func SanitizeHTMLSections(raw json.RawMessage) (json.RawMessage, int, error) {
	var root orderedObject
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw, 0, nil
	}
	pi := root.index("page_sections")
	if pi < 0 {
		return raw, 0, nil
	}
	var sections []json.RawMessage
	if err := json.Unmarshal(root[pi].val, &sections); err != nil {
		return raw, 0, nil
	}
	changed := 0
	for i, rawSec := range sections {
		var sec orderedObject
		if json.Unmarshal(rawSec, &sec) != nil {
			continue
		}
		ti, ci := sec.index("type"), sec.index("content")
		if ti < 0 || ci < 0 {
			continue
		}
		var typ string
		if json.Unmarshal(sec[ti].val, &typ) != nil || !isHTMLSectionType(typ) {
			continue
		}
		var content orderedObject
		if json.Unmarshal(sec[ci].val, &content) != nil {
			continue
		}
		bi := content.index("body")
		if bi < 0 {
			continue
		}
		var body string
		if json.Unmarshal(content[bi].val, &body) != nil {
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
	// Join by hand: json.Marshal would compact and HTML-escape every element,
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
