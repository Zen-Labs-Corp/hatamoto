package ui

// RadioCard is the hm/radiocard partial: radio choices drawn as bordered cards.
type RadioCard struct {
	Legend, Name string
	// IDPrefix keeps ids unique when a page holds several groups with the same Name.
	IDPrefix string
	Cols     int
	Items    []RadioCardItem
}

// RadioCardItem is one card; Tag is an optional badge after the title (for example "Active").
type RadioCardItem struct {
	Value, Title, Description string
	Checked                   bool
	Tag                       *Badge
	Initials                  string // decorative avatar before the title (a person)
}
