package ui

// Confirmcard is hm/confirmcard: the form and card of a confirm page. Post is
// the action the Confirm button posts to, Back where Cancel goes, Label the
// Confirm button's text (the action, never "OK"), Message the sentence in the
// card. Danger makes Confirm red. Hidden are extra fields the form posts.
type Confirmcard struct {
	Post, Back, Label, Message, CancelLabel, CSRF string
	Danger                                        bool
	Hidden                                        []ConfirmHidden
}

// ConfirmHidden is one hidden input of a confirm form.
type ConfirmHidden struct{ Name, Value string }
