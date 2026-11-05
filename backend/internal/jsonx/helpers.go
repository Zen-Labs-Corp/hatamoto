package jsonx

import (
	"bytes"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
)

// Indent marshals v with two-space indentation. Without options it uses
// Output.
func Indent(v any, o ...jsonv2.Options) ([]byte, error) {
	return jsonv2.Marshal(v, pick(Output, o), jsontext.WithIndent("  "))
}

// IndentValue re-indents already-encoded JSON with two spaces. It returns an
// error when raw is not one valid JSON value.
func IndentValue(raw []byte) ([]byte, error) {
	v := jsontext.Value(bytes.Clone(raw))
	if err := v.Indent(jsontext.WithIndent("  ")); err != nil {
		return nil, err
	}
	return v, nil
}

// Valid reports whether b is exactly one well-formed JSON value. Duplicate
// member names make it invalid, like Strict.
func Valid(b []byte) bool {
	return jsontext.Value(b).IsValid()
}

// Encode writes v to w followed by a newline. Without options it uses
// HTMLSafe, like WriteJSON.
func Encode(w io.Writer, v any, o ...jsonv2.Options) error {
	if err := jsonv2.MarshalWrite(w, v, pick(HTMLSafe, o)); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}

// Entry is one object member in document order.
type Entry struct {
	Key   string
	Value jsontext.Value
}

// ObjectEntries returns the members of the JSON object raw in document
// order. Non-object or invalid input is an error.
func ObjectEntries(raw []byte) ([]Entry, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	if tok.Kind() != '{' {
		return nil, errors.New("jsonx: not a JSON object")
	}
	var out []Entry
	for dec.PeekKind() != '}' {
		key, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		name := key.String() // the token is only valid until the next read
		val, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Key: name, Value: val.Clone()})
	}
	if _, err := dec.ReadToken(); err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		return nil, errors.New("jsonx: trailing data after object")
	}
	return out, nil
}
