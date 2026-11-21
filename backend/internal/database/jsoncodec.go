package database

import (
	"bytes"
	"context"
	"encoding/json/jsontext"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"hatamoto.dev/core/internal/jsonx"
)

// jsonTypes are json and jsonb with the pgx default codecs, except that a
// *jsontext.Value target is a byte copy: pgx would run the whole generic
// decoder only to copy the bytes. NULL handling is pgx's own (it runs before
// Unmarshal is called). Other targets and encoding use the pinned jsonx codec,
// the v1-compatible option set (the repo does not import encoding/json v1).
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
		{Name: "json", OID: pgtype.JSONOID, Codec: &pgtype.JSONCodec{Marshal: jsonx.MarshalPinned, Unmarshal: un}},
		{Name: "jsonb", OID: pgtype.JSONBOID, Codec: &pgtype.JSONBCodec{Marshal: jsonx.MarshalPinned, Unmarshal: un}},
	}
}

func registerJSONTypes(m *pgtype.Map) {
	for _, t := range jsonTypes(jsonx.UnmarshalPinned) {
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
