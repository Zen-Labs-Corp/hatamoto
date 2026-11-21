package database

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// jsonTypes are json and jsonb with the pgx default codecs, except that a
// *jsontext.Value target is a byte copy: pgx would run the whole generic
// decoder only to copy the bytes. NULL handling is pgx's own (it runs before
// Unmarshal is called).
func jsonTypes(fallback func([]byte, any) error) []*pgtype.Type {
	un := func(data []byte, v any) error {
		if p, ok := v.(*jsontext.Value); ok {
			// Copy: pgx reuses its row buffer.
			*p = jsontext.Value(bytes.Clone(bytes.Trim(data, " \t\r\n")))
			return nil
		}
		return fallback(data, v)
	}
	return []*pgtype.Type{
		{Name: "json", OID: pgtype.JSONOID, Codec: &pgtype.JSONCodec{Marshal: json.Marshal, Unmarshal: un}},
		{Name: "jsonb", OID: pgtype.JSONBOID, Codec: &pgtype.JSONBCodec{Marshal: json.Marshal, Unmarshal: un}},
	}
}

func registerJSONTypes(m *pgtype.Map) {
	for _, t := range jsonTypes(json.Unmarshal) {
		m.RegisterType(t)
	}
}

// withJSONTypes chains the registration in front of any AfterConnect already set.
func withJSONTypes(prev func(context.Context, *pgx.Conn) error) func(context.Context, *pgx.Conn) error {
	return func(ctx context.Context, c *pgx.Conn) error {
		registerJSONTypes(c.TypeMap())
		if prev != nil {
			return prev(ctx, c)
		}
		return nil
	}
}
