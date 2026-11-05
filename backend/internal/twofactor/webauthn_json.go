package twofactor

import (
	"bytes"
	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/jsonx"
)

// legacyZeroMembers are the bool and integer members of the go-webauthn
// session and credential types that carry `omitempty`. The library targets the
// v1 encoder, where omitempty drops false and 0; the v2 encoder keeps them.
// The stored session and credential bytes stay as they were by dropping those
// members when they are false or 0 (they decode back to the zero value).
var legacyZeroMembers = map[string]bool{
	"signCount":                         true,
	"cloneWarning":                      true,
	"publicKeyAlgorithm":                true,
	"authorizeUVInitialization":         true,
	"largeBlobRead":                     true,
	"largeBlobWrite":                    true,
	"enforceCredentialProtectionPolicy": true,
	"credBlob":                          true,
}

// marshalStored encodes a go-webauthn session or credential with the pinned
// options and the v1 omitempty behaviour for the members above.
func marshalStored(v any) ([]byte, error) {
	b, err := jsonx.MarshalPinned(v)
	if err != nil {
		return nil, err
	}
	dec := jsontext.NewDecoder(bytes.NewReader(b), jsonx.PinnedMarshal)
	var out bytes.Buffer
	enc := jsontext.NewEncoder(&out, jsonx.PinnedMarshal)
	if err := copyDroppingZeros(dec, enc); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

func copyDroppingZeros(dec *jsontext.Decoder, enc *jsontext.Encoder) error {
	switch dec.PeekKind() {
	case '{':
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		if err := enc.WriteToken(tok); err != nil {
			return err
		}
		for dec.PeekKind() != '}' {
			tok, err := dec.ReadToken()
			if err != nil {
				return err
			}
			name := jsontext.String(tok.String()) // tok is voided by the next read
			if legacyZeroMembers[name.String()] {
				if k := dec.PeekKind(); k == 'f' || k == '0' {
					val, err := dec.ReadValue()
					if err != nil {
						return err
					}
					if string(val) == "false" || string(val) == "0" {
						continue
					}
					if err := enc.WriteToken(name); err != nil {
						return err
					}
					if err := enc.WriteValue(val); err != nil {
						return err
					}
					continue
				}
			}
			if err := enc.WriteToken(name); err != nil {
				return err
			}
			if err := copyDroppingZeros(dec, enc); err != nil {
				return err
			}
		}
	case '[':
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		if err := enc.WriteToken(tok); err != nil {
			return err
		}
		for dec.PeekKind() != ']' {
			if err := copyDroppingZeros(dec, enc); err != nil {
				return err
			}
		}
	default:
		val, err := dec.ReadValue()
		if err != nil {
			return err
		}
		return enc.WriteValue(val)
	}
	end, err := dec.ReadToken()
	if err != nil {
		return err
	}
	return enc.WriteToken(end)
}
