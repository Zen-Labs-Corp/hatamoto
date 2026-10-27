package admin

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/siteconfig"
)

// The settings of the old admin that are folded into screens that already
// exist, plus the homepage choice.
func (a *Admin) mountMoreSettings(owner *httpx.Group) {
	owner.HandleFunc("POST /settings/general/homepage", a.handleHomepageSave)
}

type homepageData struct {
	Current, CurrentLabel, Q string
	Error                    string
}

func (a *Admin) renderHomepage(w http.ResponseWriter, r *http.Request, status int, d homepageData) {
	a.renderGeneral(w, r, status, generalData{Home: &d})
}

// homepageCurrent is the homepage choice as the form first shows it.
func (a *Admin) homepageCurrent(r *http.Request, cfg *siteconfig.Config) homepageData {
	d := homepageData{}
	if cfg.HomepageEntityID == uuid.Nil && a.deps.Growth.Explain != nil {
		// Nothing chosen yet: show the page the site serves at "/" today.
		if ex, err := a.deps.Growth.Explain.ExplainURL(r.Context(), "/"); err == nil && ex.Page.EntityID != "" {
			d.Current, d.CurrentLabel = ex.Page.EntityID, ex.Page.Name
		}
	}
	if cfg.HomepageEntityID != uuid.Nil {
		d.Current = cfg.HomepageEntityID.String()
		if a.deps.Growth.Pickers != nil {
			d.CurrentLabel = a.deps.Growth.Pickers.EntityName(r.Context(), cfg.HomepageEntityID)
		}
	}
	return d
}

func (a *Admin) handleHomepageSave(w http.ResponseWriter, r *http.Request) {
	d := homepageData{Current: r.PostFormValue("homepage"), CurrentLabel: r.PostFormValue("homepage_prev"), Q: r.PostFormValue("homepage_q")}
	id, label := a.resolvePick(r.Context(), "entity", d.Current, d.CurrentLabel, d.Q)
	d.Current, d.CurrentLabel = id, label
	eid, err := uuid.Parse(id)
	if err != nil {
		d.Error = "Choose the page that should be your homepage"
		a.renderHomepage(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	if sp, ok := a.deps.Growth.Pickers.(interface {
		EntityStatus(ctx context.Context, id uuid.UUID) (string, bool)
	}); ok {
		switch st, found := sp.EntityStatus(r.Context(), eid); {
		case !found:
			d.Error = "That page no longer exists. Choose another one."
		case st != "published":
			d.Error = "Publish that page first: visitors cannot see a " + st + " page."
		}
		if d.Error != "" {
			a.renderHomepage(w, r, http.StatusUnprocessableEntity, d)
			return
		}
	}
	if _, err := a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{HomepageEntityID: &eid}); err != nil {
		a.logger().Error("save homepage failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The homepage could not be saved.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Homepage saved.")
	http.Redirect(w, r, a.adminPath+"/settings/general#homepage", http.StatusSeeOther)
}
