package standing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"fmt"

	"hatamoto.dev/core/internal/jsonx"
)

// ManifestHash hashes the canonical form of a manifest. The manifest is
// decoded and re-encoded (map keys come out sorted) so key order and
// whitespace, which a jsonb column rewrites, do not change the hash.
func ManifestHash(raw jsontext.Value) (string, error) {
	return canonicalHash(raw)
}

// SchemaHash hashes the canonical form of an entity schema's JSON Schema.
func SchemaHash(raw jsontext.Value) (string, error) {
	return canonicalHash(raw)
}

func canonicalHash(raw jsontext.Value) (string, error) {
	var v any
	if err := jsonx.UnmarshalPinned(raw, &v); err != nil {
		return "", fmt.Errorf("failed to decode JSON for hashing: %w", err)
	}
	canon, err := jsonx.MarshalPinned(v)
	if err != nil {
		return "", fmt.Errorf("failed to encode JSON for hashing: %w", err)
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}
