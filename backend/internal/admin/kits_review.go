package admin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/kit"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/permtext"
)

// kitReviewView is the model of hm/kit-review: one screen for the wizard and Settings > Kits,
// built from a kit.Plan and the owner's ticks, never written by hand.
type kitReviewView struct {
	Title, Version, Tier, Description string
	Chips                             []ui.Badge

	Plugins      []kitReviewPlugin
	Themes       []kitReviewTheme
	ContentTypes []kitReviewItem
	Pages        []string // the pages the kit creates
	Menus        []kitReviewMenu
	Skipped      []string // steps that do not apply here, with the reason
	Messages     []string

	Homepage kitReviewHomepage
	Comments kitReviewComments
	Policy   kitReviewPolicy

	Kept, Refusals []string

	Action, CancelHref string
	Err                string
	// Fingerprint identifies the plan on screen; the form posts it back so Apply can tell the kit changed.
	Fingerprint string
}

type kitReviewPlugin struct {
	Name, Version string
	Install       bool // false: already installed, nothing to grant
	Statements    []promptStatement
	Extra         []kitReviewExtra
}

// kitReviewExtra is a permission the kit does not grant on its own: its own switch.
type kitReviewExtra struct {
	ID, Value, Label, Help string
	On                     bool
}

type kitReviewTheme struct {
	Name, Version string
	Install       bool
	Activate      bool // the kit asks to make it the active theme
	ActivateOn    bool // the switch starts ticked
}

type kitReviewItem struct{ Name, Pattern string }

type kitReviewMenu struct {
	Name  string
	Items []string
}

type kitReviewHomepage struct {
	Page string // name of the page, "" when the kit does not ask
	On   bool
}

type kitReviewComments struct {
	Requested, AlreadyOn, On bool
	Warning, Confirm         string
	NoAddress                bool
	NoAddressMsg             string
}

type kitReviewPolicy struct {
	Available, On bool
	Label         string
	Warning, Tick string
	Message       string
	AlreadyOn     string // "The community policy is already on for Stories."
}

// kitTicks are the owner's switches: from the form after a refused post, or the defaults of a fresh GET.
type kitTicks struct {
	ActivateTheme, Homepage, Comments, Policy bool
	Extra                                     []string
}

// defaultKitTicks: the theme and homepage switches start ticked on a fresh site only;
// comments and the policy are never pre-ticked (ADR 0005 rule 1).
func defaultKitTicks(pl *kit.Plan) kitTicks {
	return kitTicks{ActivateTheme: pl.Fresh, Homepage: pl.Fresh}
}

func postedKitTicks(r *http.Request) kitTicks {
	f := r.PostForm
	return kitTicks{
		ActivateTheme: f.Get("activate_theme") == "1",
		Homepage:      f.Get("homepage") == "1",
		Comments:      f.Get("comments") == "1",
		Policy:        f.Get("policy") == "1",
		Extra:         f["permission"],
	}
}

var kitActionLabels = map[kit.Action]string{kit.ActionCreate: "Create", kit.ActionInstall: "Install"}

func newKitReviewView(pl *kit.Plan, t kitTicks) kitReviewView {
	k := pl.Kit
	v := kitReviewView{Title: cmp.Or(k.Title, k.Name), Version: k.Version, Tier: pl.Tier, Description: k.Description}
	v.Chips = []ui.Badge{{Label: "v" + k.Version, Plain: true}, {Label: "signed: " + pl.Tier, Plain: true}}
	v.Kept, v.Refusals, v.Fingerprint = pl.Kept, pl.Refusals, pl.Fingerprint()
	for _, m := range pl.Messages {
		if m != pl.Policy.Message { // the policy note has its own line
			v.Messages = append(v.Messages, m)
		}
	}

	for _, st := range pl.Plugins {
		p := kitReviewPlugin{Name: st.Name, Version: st.Version, Install: st.Action == kit.ActionInstall}
		if p.Install {
			p.Statements = promptStatements(permtext.Group(st.Grant, false, false).Can, nil)
			for _, perm := range st.Extra {
				p.Extra = append(p.Extra, kitExtra(st.Name, perm, slices.Contains(t.Extra, kit.ExtraKey(st.Name, perm))))
			}
		}
		v.Plugins = append(v.Plugins, p)
	}
	for _, st := range pl.Themes {
		v.Themes = append(v.Themes, kitReviewTheme{Name: st.Name, Version: st.Version, Install: st.Action == kit.ActionInstall, Activate: st.Activate, ActivateOn: t.ActivateTheme})
	}
	for _, st := range pl.Schemas {
		switch st.Action {
		case kit.ActionCreate, kit.ActionFill:
			v.ContentTypes = append(v.ContentTypes, kitReviewItem{Name: st.Name, Pattern: kitPattern(k, st.Key)})
		case kit.ActionSkip:
			v.Skipped = append(v.Skipped, st.Name+": "+st.Note)
		}
	}
	for _, st := range pl.Pages {
		if st.Action == kit.ActionCreate {
			v.Pages = append(v.Pages, st.Name)
		}
	}
	for _, st := range pl.Menus {
		switch st.Action {
		case kit.ActionCreate:
			m := kitReviewMenu{Name: st.Name}
			if i := slices.IndexFunc(k.Menus, func(km packages.KitMenu) bool { return km.Location == st.Key }); i >= 0 {
				for _, it := range k.Menus[i].Items {
					m.Items = append(m.Items, it.Label)
				}
			}
			v.Menus = append(v.Menus, m)
		case kit.ActionSkip:
			v.Skipped = append(v.Skipped, st.Name+": "+st.Note)
		}
	}
	if pl.Homepage.Page != "" {
		v.Homepage = kitReviewHomepage{Page: pl.Homepage.Page, On: t.Homepage}
		if i := slices.IndexFunc(k.Pages, func(p packages.KitPage) bool { return p.Slug == pl.Homepage.Page }); i >= 0 {
			v.Homepage.Page = k.Pages[i].Name
		}
	}
	c := pl.Comments
	v.Comments = kitReviewComments{Requested: c.Requested, AlreadyOn: c.AlreadyOn, On: t.Comments, Warning: c.Warnings[0], Confirm: c.Warnings[1], NoAddress: c.NoAddress, NoAddressMsg: csetNoAddressMsg}
	if len(pl.Policy.Requested) > 0 || len(pl.Policy.AlreadyOn) > 0 {
		v.Policy = kitReviewPolicy{Available: pl.Policy.Available && pl.Policy.Warning != "", On: t.Policy, Warning: pl.Policy.Warning, Tick: pl.Policy.Tick, Message: pl.Policy.Message}
		v.Policy.Label = "Use the community policy on " + kitTypeNames(pl, pl.Policy.Requested) + "."
		if len(pl.Policy.AlreadyOn) > 0 {
			v.Policy.AlreadyOn = "The community policy is already on for " + kitTypeNames(pl, pl.Policy.AlreadyOn) + "."
		}
	}
	return v
}

// kitTypeNames lists content types by the name the kit gives them (the slug when it has none).
func kitTypeNames(pl *kit.Plan, slugs []string) string {
	names := make([]string, len(slugs))
	for i, slug := range slugs {
		names[i] = slug
		if j := slices.IndexFunc(pl.Kit.Schemas, func(s packages.KitSchema) bool { return s.Slug == slug }); j >= 0 && pl.Kit.Schemas[j].Name != "" {
			names[i] = pl.Kit.Schemas[j].Name
		}
	}
	return strings.Join(names, ", ")
}

// kitExtra is the switch of one permission the kit leaves to the owner (site.scripts, ui.allow:*, or
// every permission when the kit has no grant). The wording comes from permtext.
func kitExtra(plugin, perm string, on bool) kitReviewExtra {
	e := kitReviewExtra{ID: "perm-" + plugin + "-" + strings.NewReplacer(".", "-", ":", "-").Replace(perm), Value: kit.ExtraKey(plugin, perm), On: on, Label: perm}
	if can := permtext.Group([]string{perm}, false, false).Can; len(can) > 0 {
		e.Label, e.Help = can[0].Title, can[0].Sub
	}
	return e
}

// kitPattern is the content type's URL pattern with the token in angle brackets ("/item/<slug>").
func kitPattern(k *packages.Kit, slug string) string {
	i := slices.IndexFunc(k.Schemas, func(s packages.KitSchema) bool { return s.Slug == slug })
	if i < 0 {
		return ""
	}
	var s struct {
		URLPattern string `json:"url_pattern"`
	}
	if jsonx.Unmarshal(k.Schemas[i].Settings, &s, jsonx.StoredLoose) != nil {
		return ""
	}
	return strings.NewReplacer("{", "<", "}", ">").Replace(s.URLPattern)
}

// kitScreen is where a review screen is shown and posts back to: Settings > Kits or the wizard.
type kitScreen struct {
	Template   string // "settings/kits-review" or "setup/kit-review"
	Action     string // the POST address
	Cancel     string // where Cancel goes
	Steps      []ui.Step
	SectionTab bool // settings layout: highlight Settings > Kits
}

// kitSetupPage is the wizard's page data: the stepper and the review.
type kitSetupPage struct {
	Steps []ui.Step
	kitReviewView
}

// renderKitReview renders the review screen for plan with the owner's ticks (the defaults on a first visit).
func (a *Admin) renderKitReview(w http.ResponseWriter, r *http.Request, status int, scr kitScreen, pl *kit.Plan, t kitTicks, errMsg string) {
	rv := newKitReviewView(pl, t)
	rv.Action, rv.CancelHref, rv.Err = scr.Action, scr.Cancel, errMsg
	v := a.NewView(w, r, rv.Title)
	if scr.SectionTab {
		v.Section, v.Tab = "kits", "kits"
		v.Width = "compact"
	} else {
		v.Wide = true
	}
	if scr.Steps != nil {
		v.Data = kitSetupPage{Steps: scr.Steps, kitReviewView: rv}
	} else {
		v.Data = rv
	}
	a.Render(w, r, status, scr.Template, v)
}

var kitSettingsScreen = func(a *Admin, name string) kitScreen {
	return kitScreen{Template: "settings/kits-review", Action: a.adminPath + "/settings/kits/" + name, Cancel: a.adminPath + "/settings/kits", SectionTab: true}
}

func (a *Admin) kitService(w http.ResponseWriter, r *http.Request) (KitService, bool) {
	if a.deps.Kits == nil {
		a.unavailable(w, r, "Kits")
		return nil, false
	}
	return a.deps.Kits, true
}

// kitPlan reads the plan for the review, or answers the failure itself.
func (a *Admin) kitPlan(w http.ResponseWriter, r *http.Request, svc KitService, name, back string) (*kit.Plan, bool) {
	pl, err := svc.Plan(r.Context(), name)
	if err != nil || pl == nil {
		a.logger().Warn("kit plan", "kit", name, "err", err)
		a.ErrorBack(w, r, http.StatusBadGateway, "The kit could not be read", "The registry did not give this kit, or it could not be checked. Try again in a moment.", back, "Back to kits")
		return nil, false
	}
	return pl, true
}

func (a *Admin) handleKitReview(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.kitService(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	scr := kitSettingsScreen(a, name)
	pl, ok := a.kitPlan(w, r, svc, name, scr.Cancel)
	if !ok {
		return
	}
	status := http.StatusOK
	if len(pl.Refusals) > 0 {
		status = http.StatusUnprocessableEntity
	}
	a.renderKitReview(w, r, status, scr, pl, defaultKitTicks(pl), "")
}

func (a *Admin) handleKitApply(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	res, ok := a.applyKit(w, r, name, kitSettingsScreen(a, name))
	if !ok {
		return
	}
	a.SetFlash(w, r, "success", kitSummary(res))
	Redirect(w, r, a.adminPath+"/settings/kits")
}

// applyKit is the POST both entry points share. It plans once, refuses when the plan is not the one
// the owner reviewed (the form's fingerprint), refuses on the plan's refusals, and
// enforces the comments tick on the server: comments go on only with the tick and a site address. On success it audits and returns the result; on any failure it has already
// re-rendered the review and returns false.
func (a *Admin) applyKit(w http.ResponseWriter, r *http.Request, name string, scr kitScreen) (*kit.Result, bool) {
	svc, ok := a.kitService(w, r)
	if !ok {
		return nil, false
	}
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return nil, false
	}
	pl, ok := a.kitPlan(w, r, svc, name, scr.Cancel)
	if !ok {
		return nil, false
	}
	t := postedKitTicks(r)
	if r.PostForm.Get("plan") != pl.Fingerprint() {
		a.renderKitReview(w, r, http.StatusUnprocessableEntity, scr, pl, defaultKitTicks(pl), "The kit changed since you opened it. Review what it will do now, then apply it.")
		return nil, false
	}
	if len(pl.Refusals) > 0 {
		a.renderKitReview(w, r, http.StatusUnprocessableEntity, scr, pl, t, "")
		return nil, false
	}
	if t.Comments && pl.Comments.Requested && !pl.Comments.AlreadyOn && a.commentsNoAddress(r) {
		a.renderKitReview(w, r, http.StatusUnprocessableEntity, scr, pl, t, csetNoAddressMsg)
		return nil, false
	}
	acc := kit.Accepts{Comments: t.Comments, Policy: t.Policy, ActivateTheme: t.ActivateTheme, Homepage: t.Homepage, Extra: t.Extra}
	opts := kit.ApplyOptions{EnableComments: func(context.Context) error { _, err := a.enableComments(r); return err }}
	if u := userFrom(r.Context()); u != nil {
		opts.Actor = u.Email
		if id, err := uuidOf(u.ID); err == nil {
			opts.ActorID = &id
		}
	}
	res, err := svc.Apply(r.Context(), pl, acc, opts)
	if err != nil {
		var ref *kit.RefusedError
		switch {
		case errors.As(err, &ref):
			a.renderKitReview(w, r, http.StatusUnprocessableEntity, scr, pl, t, "The kit was refused: "+fullStop(strings.Join(ref.Refusals, "; ")))
		case errors.Is(err, kit.ErrNoAddress):
			a.renderKitReview(w, r, http.StatusUnprocessableEntity, scr, pl, t, csetNoAddressMsg)
		default:
			a.logger().Error("kit apply", "kit", name, "err", err)
			a.renderKitReview(w, r, http.StatusInternalServerError, scr, pl, t, "Applying the kit stopped part-way. It is safe to apply again: finished items are skipped.")
		}
		return nil, false
	}
	for _, ch := range res.PolicyOn {
		a.audit(r, "comments.policy", ch.SchemaID, map[string]any{"type": ch.Slug, "old": false, "new": true, "changed": []string{"policy"}, "via": "kit:" + name})
	}
	if res.Recorded || res.Total() > 0 || res.Filled > 0 { // a second apply that changed nothing leaves no record
		a.audit(r, "kit.apply", "kit:"+name, map[string]any{"version": pl.Kit.Version, "created": res.Total(), "filled": res.Filled, "kept": len(res.Kept), "skipped": len(res.Skipped), "comments": acc.Comments && pl.Comments.Requested && !pl.Comments.AlreadyOn})
	}
	a.invalidate(r.Context())
	return res, true
}

// kitSummary is the flash after an apply, built from what was created (like starterSummary).
func kitSummary(res *kit.Result) string {
	var parts []string
	for _, c := range []struct{ key, one, many string }{
		{"plugins", "plugin", "plugins"}, {"themes", "theme", "themes"}, {"schemas", "content type", "content types"},
		{"pages", "page", "pages"}, {"menus", "menu", "menus"},
	} {
		switch n := res.Created[c.key]; n {
		case 0:
		case 1:
			parts = append(parts, "1 "+c.one)
		default:
			parts = append(parts, fmt.Sprintf("%d %s", n, c.many))
		}
	}
	msg := "The kit was already applied. Nothing was added."
	if len(parts) > 0 {
		msg = "Kit applied: added " + strings.Join(parts, ", ") + "."
	} else if res.Filled > 0 {
		msg = "Kit applied: settings were added to existing content types."
	}
	if len(res.Messages) > 0 {
		msg += " " + strings.Join(res.Messages, " ")
	}
	return msg
}
