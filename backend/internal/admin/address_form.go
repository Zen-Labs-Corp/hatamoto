package admin

import (
	"context"
	"html/template"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/adminpath"
)

// The two ways to pick an address. addrKeep uses the private candidate the
// server drew (or the live address in the wizard); addrCustom uses the text the
// owner typed.
const (
	addrKeep   = "keep"
	addrCustom = "custom"
)

// addressForm is the picker shared by the setup wizard and the settings page:
// two radio cards, the candidate, the typed address and its strength.
type addressForm struct {
	Mode string
	// Candidate is the private address offered by the first card.
	Candidate string
	// Custom is what the owner typed for the second card.
	Custom string
	// Path is the address the current Mode selects, normalised.
	Path string
	// Origin is the scheme and host the address is shown on.
	Origin string
	Items  []ui.RadioCardItem
	// Strength is the verdict on Path, nil until it is assessed.
	Strength *strengthView
	// StrengthHTML is Strength rendered once, shared with the htmx fragment.
	StrengthHTML template.HTML
	Errors       []ui.FieldError
	// Notice is a non-blocking message, for example a failed Regenerate.
	Notice string
	// Danger is the typed confirm card, set when a weak address needs it.
	Danger *ui.DangerConfirm
}

// strengthView is how a verdict is shown: a badge and its reasons.
type strengthView struct {
	Label   string
	Kind    string
	Reasons []string
}

// customNeedsChoice answers an address typed while Keep is selected.
const customNeedsChoice = `Select "Choose my own" to use this address, or clear the field.`

// addrOutcome is the result of deciding a posted address.
type addrOutcome int

const (
	// addrOK: the address may be applied (Ack says whether it is weak).
	addrOK addrOutcome = iota
	// addrNeedPhrase: weak and no confirmation posted yet; show the card, 422.
	addrNeedPhrase
	// addrBadPhrase: weak and the typed text is wrong; card with error, 422.
	addrBadPhrase
	// addrRefused: reserved, invalid or typed under Keep; message on the form, 422.
	addrRefused
)

// addrDecision is what decideAddress tells the handler to do.
type addrDecision struct {
	Outcome addrOutcome
	Path    string
	// Ack is true when a weak address was confirmed by typing it.
	Ack     bool
	Danger  ui.DangerConfirm
	Message string
}

// userLabel is how the audit trail and the address record name a user: the
// display name, else the email.
func userLabel(u *CurrentUser) string {
	if u == nil {
		return ""
	}
	if n := strings.TrimSpace(u.Name); n != "" {
		return n
	}
	return u.Email
}

// normalizeAddress lower-cases and trims what was typed and adds the leading
// slash when it is missing. Assess judges the rest.
func normalizeAddress(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s != "" && !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

// addressItems are the two radio cards.
func addressItems(mode string) []ui.RadioCardItem {
	return []ui.RadioCardItem{
		{Value: addrKeep, Title: "Keep this private address (recommended)", Checked: mode != addrCustom},
		{Value: addrCustom, Title: "Choose my own", Checked: mode == addrCustom},
	}
}

// addressOrigin is the scheme and host shown in front of the address: the
// configured site address, else the origin the admin is open on.
func (a *Admin) addressOrigin(r *http.Request) string {
	if a.deps.BaseURL != nil {
		if u := strings.TrimRight(a.deps.BaseURL(r), "/"); u != "" {
			return u
		}
	}
	return a.requestOrigin(r)
}

// newAddressForm is the picker before anything was posted: the candidate is
// selected and the custom field is empty.
func (a *Admin) newAddressForm(r *http.Request, candidate string) *addressForm {
	return a.finishAddressForm(r, &addressForm{Mode: addrKeep, Candidate: candidate})
}

// parseAddressForm reads the picker from a posted form (or query).
func (a *Admin) parseAddressForm(r *http.Request) *addressForm {
	f := &addressForm{
		Mode:      addrKeep,
		Candidate: normalizeAddress(r.FormValue("candidate")),
		Custom:    strings.TrimSpace(r.FormValue("custom")),
	}
	if r.FormValue("mode") == addrCustom {
		f.Mode = addrCustom
	}
	return a.finishAddressForm(r, f)
}

func (a *Admin) finishAddressForm(r *http.Request, f *addressForm) *addressForm {
	f.Path = f.Candidate
	if f.Mode == addrCustom {
		f.Path = normalizeAddress(f.Custom)
	}
	f.Origin = a.addressOrigin(r)
	f.Items = addressItems(f.Mode)
	return f
}

// evaluateAddress assesses f.Path and fills f.Strength. An empty custom address
// has no verdict yet.
func (a *Admin) evaluateAddress(ctx context.Context, f *addressForm) adminpath.Result {
	if f.Path == "" {
		f.Strength = nil
		return adminpath.Result{}
	}
	res := a.deps.Address.Assess(ctx, f.Path)
	f.Strength = strengthOf(res)
	return res
}

// strengthOf turns a verdict into the badge and reasons.
func strengthOf(res adminpath.Result) *strengthView {
	s := &strengthView{Label: res.Level.String()}
	switch res.Level {
	case adminpath.LevelStrong:
		s.Kind = "success"
	case adminpath.LevelOK:
		s.Kind = "info"
	case adminpath.LevelWeak:
		s.Kind = "warning"
	default:
		s.Kind = "danger"
	}
	for _, r := range res.Reasons {
		text := r.Text
		if r.Code == "scanner" {
			text = "on the list of addresses attack tools try"
		}
		s.Reasons = append(s.Reasons, text)
	}
	return s
}

// scannerExamples are the addresses the typed confirm names as the ones bots
// try, each written as code. They are built from segments so no admin address
// is spelled in this file.
func scannerExamples() []ui.DangerPart {
	segs := []string{"admin", "wp-admin", "login"}
	var parts []ui.DangerPart
	for i, s := range segs {
		switch {
		case i == len(segs)-1:
			parts = append(parts, ui.DangerPart{Text: " and "})
		case i > 0:
			parts = append(parts, ui.DangerPart{Text: ", "})
		}
		parts = append(parts, ui.DangerPart{Text: "/" + s, Code: true})
	}
	return parts
}

// addressDanger is the typed confirm for a weak address. The phrase, title,
// prompt and button all carry the address being chosen. action is where the
// card posts; back is its Cancel link; hidden re-posts the picker.
func (a *Admin) addressDanger(r *http.Request, path, action, back string, hidden []ui.ConfirmHidden) ui.DangerConfirm {
	intro := append([]ui.DangerPart{{Text: "Bots scan the whole internet for "}}, scannerExamples()...)
	intro = append(intro, ui.DangerPart{Text: " every day. On a guessable address they hammer your login page with password guesses and probe for exploits, and they learn which software you run. With a private address, they get a plain \"page not found\"."})
	return ui.DangerConfirm{
		ID:     "dc-admin-address",
		Title:  path + " is the first address attack tools try",
		Phrase: path,
		Prompt: "To use this address anyway, type " + path + " below.",
		Action: action,
		Back:   back,
		Label:  "I understand, use " + path,
		CSRF:   csrfToken(r.Context()),
		Hidden: hidden,
		Body: []ui.DangerText{
			{Parts: intro},
			{Parts: []ui.DangerPart{{Text: "Your login is still protected by your password, rate limits and two-factor authentication. A private address keeps the bots away from it in the first place."}}},
		},
	}
}

// pickerHidden carries the picker through the typed confirm.
func pickerHidden(f *addressForm) []ui.ConfirmHidden {
	return []ui.ConfirmHidden{{Name: "mode", Value: f.Mode}, {Name: "candidate", Value: f.Candidate}, {Name: "custom", Value: f.Custom}}
}

// decideAddress judges a posted picker. A strong or ok address passes. A weak
// one needs the typed confirmation: none posted yet is addrNeedPhrase, a wrong
// one addrBadPhrase, the exact address passes with Ack. Reserved and invalid
// addresses are refused. The card posts to action and cancels to back.
func (a *Admin) decideAddress(r *http.Request, f *addressForm, action, back string) addrDecision {
	_ = r.ParseForm()
	// A typed address with Keep selected would be dropped without a word.
	if f.Mode == addrKeep && f.Custom != "" {
		return addrDecision{Outcome: addrRefused, Message: customNeedsChoice}
	}
	res := a.evaluateAddress(r.Context(), f)
	d := addrDecision{Path: res.Path}
	if d.Path == "" {
		d.Path = f.Path
	}
	switch res.Level {
	case adminpath.LevelStrong, adminpath.LevelOK:
		return d
	case adminpath.LevelWeak:
		dc := a.addressDanger(r, d.Path, action, back, pickerHidden(f))
		if !r.PostForm.Has("confirm_phrase") {
			d.Outcome, d.Danger = addrNeedPhrase, dc
			return d
		}
		dc, ok := CheckDangerPhrase(r, dc)
		d.Danger = dc
		if !ok {
			d.Outcome = addrBadPhrase
			return d
		}
		d.Ack = true
		return d
	}
	d.Outcome = addrRefused
	d.Message = "That address cannot be used."
	if len(res.Reasons) > 0 {
		d.Message = sentence(res.Reasons[0].Text)
	}
	return d
}
