package ui

// Savestate is the hm/savestate partial: a dot plus text for an editor's save status.
// State is one of saved, unsaved, saving, error or idle; scripts only change data-state and Text.
type Savestate struct {
	State string
	Text  string
}
