package ui

// Taginput is the hm/taginput partial: chips plus one text input. Key binds it to a settings
// field (data-hm-fs); js/ui/taginput.js renders the chips from the template.
type Taginput struct {
	ID          string
	Key         string
	Label       string
	Placeholder string
	Hint        string
}
