package server

import (
	"encoding/json"

	"hatamoto.dev/core/internal/seo"
)

// attrsNoIndex is seo.EntryNoIndex for attributes alone.
func attrsNoIndex(attrs json.RawMessage) bool { return seo.EntryNoIndex(nil, attrs) }
