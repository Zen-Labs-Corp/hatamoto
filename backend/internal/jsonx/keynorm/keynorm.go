// Package keynorm rewrites case-variant object keys of a JSON document to the
// canonical names of a Shape. Version 1 of encoding/json matched object names
// case-insensitively and version 2 does not, so stored documents are
// normalised once before they are read strictly.
//
// The package is pure: standard library only, no database. Values are copied
// as raw JSON, so numbers and strings that need no change keep their exact
// bytes.
package keynorm

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Shape describes the keys of a JSON value. A nil *Shape is an opaque value
// that is copied verbatim.
type Shape struct {
	// Fields maps canonical object keys to the shape of their value (nil is
	// opaque). Keys that match no canonical name, even ignoring case, pass
	// through untouched.
	Fields map[string]*Shape
	// Elem is the shape of array elements, or of the values of an Open object.
	Elem *Shape
	// Open marks an object whose keys are free (a map): they are never
	// renamed, only their values are normalised with Elem.
	Open bool
}

// Change is one rewrite Normalise made or would make.
type Change struct {
	// Path is the JSON Pointer of the object holding the key ("" for the root).
	Path string
	// From is the key as stored.
	From string
	// To is the canonical key it became. When Dropped is set To is the key
	// that won the collision and From was removed.
	To      string
	Dropped bool
	// Value is the raw JSON of the removed member when Dropped is set.
	Value string
}

func (c Change) String() string {
	if c.Dropped {
		v := c.Value
		if len(v) > 200 {
			v = v[:200] + "..."
		}
		return fmt.Sprintf("%s/%s dropped (collides with %s, value %s)", c.Path, escape(c.From), c.To, v)
	}
	return fmt.Sprintf("%s/%s -> %s", c.Path, escape(c.From), c.To)
}

// Normalise rewrites case-variant keys of raw to the canonical names of s. If
// several keys of one object fold to the same canonical name, the last of them
// in input order wins and is renamed; the others are dropped and reported with
// Dropped, with their value. That is the value version 1 read, which decoded
// members in order and let a later case-insensitive match overwrite an earlier
// one (callers read jsonb as text, whose key order is length, then bytes). When nothing changes the input slice is
// returned as is, so a second run is a no-op.
func Normalise(raw []byte, s *Shape) (out []byte, changes []Change, err error) {
	if s == nil {
		return raw, nil, nil
	}
	v, ch, err := walk(jsontext.Value(raw), s, "")
	if err != nil {
		return nil, nil, err
	}
	if len(ch) == 0 {
		return raw, nil, nil
	}
	return v, ch, nil
}

type member struct {
	name string
	val  jsontext.Value
}

func walk(raw jsontext.Value, s *Shape, path string) (jsontext.Value, []Change, error) {
	if s == nil {
		return raw, nil, nil
	}
	switch raw.Kind() {
	case '{':
		if s.Fields == nil && !s.Open {
			return raw, nil, nil
		}
		return walkObject(raw, s, path)
	case '[':
		if s.Elem == nil {
			return raw, nil, nil
		}
		return walkArray(raw, s, path)
	}
	return raw, nil, nil
}

func walkArray(raw jsontext.Value, s *Shape, path string) (jsontext.Value, []Change, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(true))
	if _, err := dec.ReadToken(); err != nil { // '['
		return nil, nil, err
	}
	var (
		elems   []jsontext.Value
		changes []Change
		changed bool
	)
	for i := 0; dec.PeekKind() != ']'; i++ {
		v, err := dec.ReadValue()
		if err != nil {
			return nil, nil, err
		}
		nv, ch, err := walk(v.Clone(), s.Elem, fmt.Sprintf("%s/%d", path, i))
		if err != nil {
			return nil, nil, err
		}
		if len(ch) > 0 {
			changed = true
			changes = append(changes, ch...)
		}
		elems = append(elems, nv)
	}
	if !changed {
		return raw, nil, nil
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, e := range elems {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(e)
	}
	buf.WriteByte(']')
	return buf.Bytes(), changes, nil
}

func walkObject(raw jsontext.Value, s *Shape, path string) (jsontext.Value, []Change, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(true))
	if _, err := dec.ReadToken(); err != nil { // '{'
		return nil, nil, err
	}
	var members []member
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, nil, err
		}
		name := tok.String() // valid only until the next decoder call
		v, err := dec.ReadValue()
		if err != nil {
			return nil, nil, err
		}
		members = append(members, member{name: name, val: v.Clone()})
	}

	var changes []Change
	if !s.Open {
		var ch []Change
		members, ch = canonicalise(members, s, path)
		changes = append(changes, ch...)
	}
	for i := range members {
		child := s.Elem
		if !s.Open {
			child = s.Fields[members[i].name]
		}
		nv, ch, err := walk(members[i].val, child, path+"/"+escape(members[i].name))
		if err != nil {
			return nil, nil, err
		}
		if len(ch) > 0 {
			members[i].val = nv
			changes = append(changes, ch...)
		}
	}
	if len(changes) == 0 {
		return raw, nil, nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := jsontext.AppendQuote(nil, m.name)
		if err != nil {
			return nil, nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(m.val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), changes, nil
}

// canonicalise renames case variants to their canonical key and removes the
// collision losers. Member order is kept; a renamed winner stays in place.
func canonicalise(members []member, s *Shape, path string) ([]member, []Change) {
	// Sorted so that two fold-equal canonical names resolve the same way on
	// every run: an exact match first, else the smallest canonical name.
	fieldNames := make([]string, 0, len(s.Fields))
	for c := range s.Fields {
		fieldNames = append(fieldNames, c)
	}
	sort.Strings(fieldNames)
	canon := func(name string) (string, bool) {
		if _, ok := s.Fields[name]; ok {
			return name, true
		}
		for _, c := range fieldNames {
			if strings.EqualFold(c, name) {
				return c, true
			}
		}
		return "", false
	}

	groups := map[string][]int{} // canonical name -> member indexes
	for i, m := range members {
		if c, ok := canon(m.name); ok {
			groups[c] = append(groups[c], i)
		}
	}

	drop := map[int]bool{}
	rename := map[int]string{}
	var changes []Change
	names := make([]string, 0, len(groups))
	for c := range groups {
		names = append(names, c)
	}
	sort.Strings(names)
	for _, c := range names {
		idx := groups[c]
		win := idx[len(idx)-1] // last member wins, as v1 read it
		if members[win].name != c {
			rename[win] = c
			changes = append(changes, Change{Path: path, From: members[win].name, To: c})
		}
		for _, i := range idx {
			if i != win {
				drop[i] = true
				changes = append(changes, Change{Path: path, From: members[i].name, To: c, Dropped: true, Value: string(members[i].val)})
			}
		}
	}
	if len(changes) == 0 {
		return members, nil
	}
	out := make([]member, 0, len(members))
	for i, m := range members {
		if drop[i] {
			continue
		}
		if c, ok := rename[i]; ok {
			m.name = c
		}
		out = append(out, m)
	}
	return out, changes
}

func escape(name string) string {
	name = strings.ReplaceAll(name, "~", "~0")
	return strings.ReplaceAll(name, "/", "~1")
}

type jsonUnmarshaler interface{ UnmarshalJSON([]byte) error }

var unmarshalerType = reflect.TypeFor[jsonUnmarshaler]()

// ShapeOf derives a Shape from the json tags of t, for tests and the doctor.
// Types with their own UnmarshalJSON, scalars and cyclic types are opaque
// (nil). Migrations freeze literal shapes instead of calling this.
func ShapeOf(t reflect.Type) *Shape {
	return shapeOf(t, map[reflect.Type]bool{})
}

func shapeOf(t reflect.Type, seen map[reflect.Type]bool) *Shape {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Implements(unmarshalerType) || reflect.PointerTo(t).Implements(unmarshalerType) {
		return nil
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if e := shapeOf(t.Elem(), seen); e != nil {
			return &Shape{Elem: e}
		}
	case reflect.Map:
		if e := shapeOf(t.Elem(), seen); e != nil {
			return &Shape{Open: true, Elem: e}
		}
	case reflect.Struct:
		if seen[t] {
			return nil
		}
		seen[t] = true
		defer delete(seen, t)
		s := &Shape{Fields: map[string]*Shape{}}
		addFields(s, t, seen)
		return s
	}
	return nil
}

func addFields(s *Shape, t reflect.Type, seen map[reflect.Type]bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, hasTag := f.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" && !strings.Contains(tag, ",") {
			continue
		}
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				addFields(s, ft, seen)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if !hasTag || name == "" {
			name = f.Name
		}
		s.Fields[name] = shapeOf(f.Type, seen)
	}
}
