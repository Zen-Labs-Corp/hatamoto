package ui

// DangerConfirm is the hm/danger-confirm partial: an inline danger card with
// a typed phrase. The server always checks the posted confirm_phrase (see
// admin.CheckDangerPhrase); the submit state in the browser is only a hint.
type DangerConfirm struct {
	// ID keeps element ids unique when several cards share a page.
	ID    string
	Title string
	// Phrase is what the user must type, compared exactly.
	Phrase string
	// Prompt is the visible label of the phrase input.
	Prompt string
	// Action is the form post target; Back is the Cancel link.
	Action string
	Back   string
	// Label is the danger submit text; CancelLabel defaults to "Cancel".
	Label       string
	CancelLabel string
	// Tone "ink" draws the submit as the filled ink primary with a ghost
	// Cancel; empty keeps the red danger submit.
	Tone string
	CSRF string
	// Error is set by the server helper after a failed check.
	Error string
	Body  []DangerText
	// Hidden carries extra fields the caller needs re-posted.
	Hidden []ConfirmHidden
}

// DangerText is one body paragraph made of segments.
type DangerText struct{ Parts []DangerPart }

// DangerPart is one text segment, optionally rendered as code or strong.
type DangerPart struct {
	Text         string
	Code, Strong bool
}
