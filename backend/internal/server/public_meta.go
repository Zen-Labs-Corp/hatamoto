package server

import (
	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/seo"
)

// attrsNoIndex is seo.EntryNoIndex for attributes alone.
func attrsNoIndex(attrs jsontext.Value) bool { return seo.EntryNoIndex(nil, attrs) }
