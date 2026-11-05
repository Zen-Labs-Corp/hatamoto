// Package jsonx holds the named encoding/json/v2 option sets and thin
// helpers the backend shares. The package is a leaf: standard library only,
// and it never imports the v1 encoding/json package.
//
// Option sets:
//
//   - Strict: request input. Duplicate names, invalid UTF-8 and trailing data
//     are rejected (v2 defaults). Names match case-sensitively, so a wrong-case
//     name is silently ignored; use StrictNoUnknown to turn that into an error.
//   - StrictNoUnknown: Strict plus RejectUnknownMembers.
//   - Stored: reads of stored documents. Case-sensitive, unknown members are
//     ignored.
//   - Output: API responses. Map keys are sorted, nil slices and maps are
//     written as [] and {}.
//   - HTMLSafe: Output plus escaping of <, > and & for JSON embedded in HTML.
//   - Pinned (pinned.go): wire formats that must keep their historic bytes.
package jsonx

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
)

var (
	// Strict rejects duplicate names, invalid UTF-8 and trailing data.
	Strict = jsonv2.DefaultOptionsV2()
	// StrictNoUnknown is Strict that also rejects unknown (or wrong-case)
	// object members.
	StrictNoUnknown = jsonv2.JoinOptions(Strict, jsonv2.RejectUnknownMembers(true))
	// Stored reads persisted documents: case-sensitive, unknown members ignored.
	Stored = jsonv2.DefaultOptionsV2()
	// Output writes deterministic (sorted) maps; nil slices and maps become
	// [] and {}.
	Output = jsonv2.JoinOptions(jsonv2.Deterministic(true))
	// HTMLSafe is Output with <, > and & escaped, and U+2028 and U+2029 too,
	// like the v1 encoders.
	HTMLSafe = jsonv2.JoinOptions(Output, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
)

// Unmarshal decodes in into v. Without options it uses Strict. Trailing data
// after the first value is always an error; trailing whitespace is fine.
func Unmarshal(in []byte, v any, o ...jsonv2.Options) error {
	return jsonv2.Unmarshal(in, v, pick(Strict, o))
}

// UnmarshalRead decodes one value from r, reading to EOF. Without options it
// uses Strict.
func UnmarshalRead(r io.Reader, v any, o ...jsonv2.Options) error {
	return jsonv2.UnmarshalRead(r, v, pick(Strict, o))
}

// Marshal encodes v. Without options it uses Output.
func Marshal(v any, o ...jsonv2.Options) ([]byte, error) {
	return jsonv2.Marshal(v, pick(Output, o))
}

// IsUnknownMember reports whether err is a rejected unknown object member.
func IsUnknownMember(err error) bool {
	return errors.Is(err, jsonv2.ErrUnknownName)
}

// ErrorPointer returns the JSON Pointer of the value a decode error refers
// to, or "" when the error carries none.
func ErrorPointer(err error) string {
	var se *jsonv2.SemanticError
	if errors.As(err, &se) {
		return string(se.JSONPointer)
	}
	var sy *jsontext.SyntacticError
	if errors.As(err, &sy) {
		return string(sy.JSONPointer)
	}
	return ""
}

func pick(def jsonv2.Options, o []jsonv2.Options) jsonv2.Options {
	if len(o) == 0 {
		return def
	}
	return jsonv2.JoinOptions(o...)
}
