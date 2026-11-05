package jsonx

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// ExactNumbers is Stored, except that every JSON number inside an `any`
// decodes as its raw text (a jsontext.Value), so a document rewritten by an
// editor keeps digits a float64 would round, such as 9007199254740993.
// Objects and arrays recurse through the same option; the other kinds decode
// as usual.
var ExactNumbers = exactNumbers(Stored)

// ExactNumbersLoose is ExactNumbers that also accepts duplicate names (the
// last value wins, as in jsonb), for sanitising entity attributes.
var ExactNumbersLoose = exactNumbers(jsonv2.JoinOptions(Stored, jsontext.AllowDuplicateNames(true)))

func exactNumbers(base jsonv2.Options) jsonv2.Options {
	var opts jsonv2.Options
	opts = jsonv2.JoinOptions(base, jsonv2.WithUnmarshalers(jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, v *any) error {
		switch dec.PeekKind() {
		case '0':
			n, err := dec.ReadValue()
			if err != nil {
				return err
			}
			*v = n.Clone()
		case '{':
			m := map[string]any{}
			if err := jsonv2.UnmarshalDecode(dec, &m, opts); err != nil {
				return err
			}
			*v = m
		case '[':
			l := []any{}
			if err := jsonv2.UnmarshalDecode(dec, &l, opts); err != nil {
				return err
			}
			*v = l
		default:
			// a scalar: decode its text with the plain options (the decoder
			// would hand an `any` straight back to this function)
			raw, err := dec.ReadValue()
			if err != nil {
				return err
			}
			var x any
			if err := jsonv2.Unmarshal(raw, &x, base); err != nil {
				return err
			}
			*v = x
		}
		return nil
	})))
	return opts
}
