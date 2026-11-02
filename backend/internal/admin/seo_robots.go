package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

const seoRobotsPath = "/settings/seo/robots"

// robotsState is what the Robots tab edits, as stored or as submitted.
type robotsState struct {
	Set seo.Robots
	// MaxSnippet is the typed text of the max-snippet box, kept so a rejected
	// value is shown back as typed.
	MaxSnippet string
	// Errors found while reading the form, before Validate runs.
	Errs []seo.FieldError
}

type seoRobotsData struct {
	Summary  []ui.FieldError
	Errs     map[string]string
	State    robotsState
	Previews []ui.Option
	Lines    string
	MaxLines int
	// Preview is the robots.txt the current values produce; empty hides the card.
	Preview string
	// AI is the crawler policy form; nil when the analytics module is not mounted.
	AI *seoRobotsAI
}

// seoRobotsAI is the AI-crawler policy card, which posts to the analytics endpoint.
type seoRobotsAI struct {
	Form     ui.AiaPolicyForm
	Purposes []seoRobotsPurpose
}

type seoRobotsPurpose struct {
	Name, Description, Tokens string
	Policy                    *ui.AiaPolicy
}

func seoRobotsStored(set seo.Settings) robotsState {
	st := robotsState{Set: set.Robots}
	if set.Robots.MaxSnippet != nil {
		st.MaxSnippet = strconv.Itoa(*set.Robots.MaxSnippet)
	}
	return st
}

// seoRobotsFromForm applies the posted fields over stored. Fields the post does
// not carry keep their stored value (a switch posts "true" or "false").
func seoRobotsFromForm(form url.Values, stored robotsState) robotsState {
	next := robotsState{Set: stored.Set, MaxSnippet: stored.MaxSnippet}
	next.Set.ExtraLines = append([]string(nil), stored.Set.ExtraLines...)
	if form.Has("max_snippet") {
		next.MaxSnippet = strings.TrimSpace(form.Get("max_snippet"))
		next.Set.MaxSnippet = nil
		if next.MaxSnippet != "" {
			n, err := strconv.Atoi(next.MaxSnippet)
			if err != nil {
				next.Errs = append(next.Errs, seo.FieldError{Field: "robots[max_snippet]", Message: "Use -1, 0 or a positive number."})
			} else {
				next.Set.MaxSnippet = &n
			}
		}
	}
	if form.Has("max_image_preview") {
		next.Set.MaxImagePreview = form.Get("max_image_preview")
	}
	if form.Has("noarchive") {
		next.Set.NoArchive = form.Get("noarchive") == "true"
	}
	if form.Has("nosnippet") {
		next.Set.NoSnippet = form.Get("nosnippet") == "true"
	}
	if form.Has("extra_lines") {
		next.Set.ExtraLines = nil
		for _, l := range strings.Split(form.Get("extra_lines"), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				next.Set.ExtraLines = append(next.Set.ExtraLines, l)
			}
		}
	}
	return next
}

// seoRobotsErrors lists the problems of a submitted state, each once.
func (a *Admin) seoRobotsErrors(set seo.Settings, st robotsState) []ui.FieldError {
	probe := set.Clone()
	probe.Robots = st.Set
	found := append([]seo.FieldError(nil), st.Errs...)
	for _, fe := range probe.Validate() {
		if strings.HasPrefix(fe.Field, "robots") {
			found = append(found, fe)
		}
	}
	found = append(found, seo.ValidateRobotsLines(st.Set.ExtraLines, a.adminPath)...)
	var errs []ui.FieldError
	seen := map[string]bool{}
	for _, fe := range found {
		if k := fe.Field; !seen[k] {
			seen[k] = true
			errs = append(errs, ui.FieldError{ID: seoFieldID(fe.Field), Label: seoFieldLabel(fe.Field), Message: fe.Message})
		}
	}
	return errs
}

func (a *Admin) handleSEORobots(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEORobots(w, r, http.StatusOK, seoRobotsStored(set), nil)
}

func (a *Admin) handleSEORobotsSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	next := seoRobotsFromForm(r.PostForm, seoRobotsStored(set))
	if errs := a.seoRobotsErrors(set, next); len(errs) > 0 {
		a.renderSEORobots(w, r, http.StatusUnprocessableEntity, next, errs)
		return
	}
	n := next.Set
	if !a.seoSave(w, r, func(s *seo.Settings) { s.Robots = n }, map[string]any{"tab": "robots", "extra_lines": len(n.ExtraLines)}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoRobotsPath, http.StatusSeeOther)
}

func (a *Admin) renderSEORobots(w http.ResponseWriter, r *http.Request, status int, st robotsState, errs []ui.FieldError) {
	d := seoRobotsData{
		Summary: errs, Errs: map[string]string{}, State: st, Lines: strings.Join(st.Set.ExtraLines, "\n"),
		MaxLines: seo.MaxRobotsLines,
	}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	for _, p := range seo.MaxImagePreviews {
		label := p
		if p == "" {
			label = "Not set"
		}
		d.Previews = append(d.Previews, ui.Option{Value: p, Label: label, Selected: p == st.Set.MaxImagePreview})
	}
	if a.deps.RobotsPreview != nil {
		d.Preview = a.deps.RobotsPreview(r, st.Set)
	}
	d.AI = a.seoRobotsAIForm(r)

	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-robots"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-robots", v)
}

// seoRobotsAIForm is the crawler policy as Analytics > Crawlers shows it, or
// nil when that module is not mounted or its store fails.
func (a *Admin) seoRobotsAIForm(r *http.Request) *seoRobotsAI {
	form, err := a.robotsData(r.Context(), nil, nil)
	if err != nil {
		if err != errNoService {
			a.logger().Error("robots policy", "err", err)
		}
		return nil
	}
	u := userFrom(r.Context())
	owner := u != nil && u.Role == "owner"
	policies := aiaPolicies(form, owner)
	ai := &seoRobotsAI{Form: ui.AiaPolicyForm{CSRF: csrfToken(r.Context()), Return: a.adminPath + seoRobotsPath, Owner: owner}}
	for _, row := range form.Rows {
		ai.Purposes = append(ai.Purposes, seoRobotsPurpose{
			Name: row.Label, Description: purposeDescriptions[row.Purpose], Tokens: row.Tokens, Policy: policies[row.Purpose],
		})
	}
	return ai
}
