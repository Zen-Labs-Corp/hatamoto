package server

import (
	"encoding/json"
	"strings"
)

// attrsNoIndex reports whether entity attributes ask search engines to skip
// the page: either attributes.no_index is true or attributes.seo.robots
// contains "noindex" (set by the SEO schema tab). Invalid JSON is not noindex.
func attrsNoIndex(attrs json.RawMessage) bool {
	var a struct {
		NoIndex bool `json:"no_index"`
		SEO     struct {
			Robots string `json:"robots"`
		} `json:"seo"`
	}
	if json.Unmarshal(attrs, &a) != nil {
		return false
	}
	return a.NoIndex || strings.Contains(strings.ToLower(a.SEO.Robots), "noindex")
}
