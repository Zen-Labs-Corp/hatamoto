package ui

// Segmented is the hm/segmented partial. Mode "" is a form whose buttons post one value each,
// "radio" a fieldset of radios a surrounding form posts, "buttons" plain buttons a script toggles.
type Segmented struct {
	Mode   string
	Label  string
	Action string
	Name   string // required in post and radio modes; unused in buttons mode
	CSRF   string
	Attrs  map[string]string // on the container (every mode)
	Items  []SegmentedItem
}

// SegmentedItem is one button of a Segmented; Pressed marks the current value. Attrs are script
// hooks on the button or, in radio mode, the input (every mode).
type SegmentedItem struct {
	Label, Value string
	Pressed      bool
	Attrs        map[string]string
}
