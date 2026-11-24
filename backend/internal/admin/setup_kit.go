package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// The wizard's kit flow, reached from the Content step's "Start from a kit" card. It keeps the
// wizard's step numbers: the list and the review both sit on the Content step. Setup stays open
// until the kit is applied, so a refused or failed apply leaves the wizard where it was.

func (a *Admin) mountSetupKit(owner *httpx.Group) {
	owner.HandleFunc("GET /setup/kit", a.handleSetupKitList)
	owner.HandleFunc("POST /setup/kit", a.handleSetupKitPick)
	owner.HandleFunc("GET /setup/kit/{name}", a.handleSetupKitReview)
	owner.HandleFunc("POST /setup/kit/{name}", a.handleSetupKitApply)
}

// setupKitScreen is the review shown inside the wizard; Cancel goes back to the Content step.
func (a *Admin) setupKitScreen(name string) kitScreen {
	return kitScreen{Template: "setup/kit-review", Action: a.adminPath + "/setup/kit/" + name, Cancel: a.adminPath + "/setup/content", Steps: steps(5)}
}

type setupKitData struct {
	Steps  []ui.Step
	Items  []ui.RadioCardItem
	Failed string
	Back   string
}

func (a *Admin) renderSetupKits(w http.ResponseWriter, r *http.Request, status int, failed string) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	d := setupKitData{Steps: steps(5), Failed: failed, Back: a.adminPath + "/setup/content"}
	if a.deps.Kits != nil {
		if _, available, err := a.deps.Kits.List(ctx); err == nil {
			for i, e := range available {
				d.Items = append(d.Items, ui.RadioCardItem{Value: e.Name, Title: strings.TrimSpace(e.Title + " " + e.Version), Description: e.Description, Checked: i == 0, Tag: &ui.Badge{Label: "signed: " + e.Tier, Plain: true}})
			}
		}
	}
	v := a.NewView(w, r, "Choose a kit")
	v.Wide = true
	v.Data = d
	a.Render(w, r, status, "setup/kit", v)
}

func (a *Admin) handleSetupKitList(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionKitApply) {
		return
	}
	if a.deps.Kits == nil {
		a.unavailable(w, r, "Kits")
		return
	}
	a.renderSetupKits(w, r, http.StatusOK, "")
}

// handleSetupKitPick sends the chosen kit to its review.
func (a *Admin) handleSetupKitPick(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionKitApply) {
		return
	}
	name := r.PostFormValue("kit")
	if name == "" || strings.ContainsAny(name, "/?#") {
		a.renderSetupKits(w, r, http.StatusUnprocessableEntity, "Choose a kit.")
		return
	}
	http.Redirect(w, r, a.adminPath+"/setup/kit/"+name, http.StatusSeeOther)
}

func (a *Admin) handleSetupKitReview(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionKitApply) {
		return
	}
	svc, ok := a.kitService(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	scr := a.setupKitScreen(name)
	pl, ok := a.kitPlan(w, r, svc, name, a.adminPath+"/setup/kit")
	if !ok {
		return
	}
	status := http.StatusOK
	if len(pl.Refusals) > 0 {
		status = http.StatusUnprocessableEntity
	}
	a.renderKitReview(w, r, status, scr, pl, defaultKitTicks(pl), "")
}

// handleSetupKitApply applies the kit, then finishes setup: the kit goes first so its menus win over
// the core seed (Decision 14).
func (a *Admin) handleSetupKitApply(w http.ResponseWriter, r *http.Request) {
	if !a.requireCan(w, r, authz.ActionKitApply) {
		return
	}
	res, ok := a.applyKit(w, r, r.PathValue("name"), a.setupKitScreen(r.PathValue("name")))
	if !ok {
		return
	}
	a.SetFlash(w, r, "success", kitSummary(res))
	if err := a.finishSetup(r); err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The kit is applied, but we could not finish setup. Open the setup page again to finish.")
		return
	}
	http.Redirect(w, r, a.adminPath+"/setup/done", http.StatusSeeOther)
}
