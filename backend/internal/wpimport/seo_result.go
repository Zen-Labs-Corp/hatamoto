package wpimport

// SEORow is one global SEO setting: Current is empty in Applied rows.
type SEORow struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Current string `json:"current,omitempty"`
	Value   string `json:"value"`
}

// SEOResult is what the SEO settings step did.
type SEOResult struct {
	Plugin    string   `json:"plugin,omitempty"`  // display key: yoast|rankmath|tsf|seopress|aioseo
	Sources   []string `json:"sources,omitempty"` // "live", "file:rankmath"...
	Applied   []SEORow `json:"applied,omitempty"` // ModeEmpty
	Would     []SEORow `json:"would,omitempty"`   // ModeExisting
	NotMapped []string `json:"not_mapped,omitempty"`
}

// SEOFile is a parsed plugin settings export: the plugin's own option names.
type SEOFile struct {
	Plugin string
	Values map[string]any
}
