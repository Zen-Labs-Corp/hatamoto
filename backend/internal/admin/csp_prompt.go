package admin

import (
	"context"
	"net/http"

	"encoding/json/jsontext"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
)

// CSPState is the live page protection mode of the public site.
type CSPState struct {
	Enforcing bool // the public pages send an enforcing Content-Security-Policy
	EnvForced bool // CSP_ENFORCE is set in the environment; the stored choice is ignored
}

// CSPService reads and switches the public page protection; the server
// implements it. Nil hides the Overview card.
type CSPService interface {
	CSPState() CSPState
	EnforceCSP(ctx context.Context) error
}

const (
	// cspHiddenKey is the theme_extra key that hides the Overview card for the whole site.
	cspHiddenKey   = "csp_prompt_hidden"
	cspEnforcePath = "/security/page-protection/enforce"
	cspHidePath    = "/security/page-protection/hide"
	cspDocsURL     = "https://hatamoto.dev/docs/operate/hardening"
	cspCardTitle   = "Turn on stricter page protection"
)

// cspCard is the model of the Overview's page protection card.
type cspCard struct{ EnforceHref, HideHref, DocsHref string }

// cspPromptHidden reports whether an owner dismissed the card. A missing or
// unreadable value counts as not dismissed.
func cspPromptHidden(cfg *siteconfig.Config) bool {
	if cfg == nil || len(cfg.ThemeExtra) == 0 {
		return false
	}
	var top map[string]jsontext.Value
	if jsonx.Unmarshal(cfg.ThemeExtra, &top, jsonx.StoredLoose) != nil {
		return false
	}
	var hidden bool
	_ = jsonx.Unmarshal(top[cspHiddenKey], &hidden, jsonx.StoredLoose)
	return hidden
}

// cspJob is the page protection card, for owners who manage settings.
func (a *Admin) cspJob() *dashJob {
	return &dashJob{Name: "page protection", SecPos: 11, Run: a.dashCSP,
		Fallback: dashSection{ID: "dash-csp", Title: cspCardTitle, ErrMsg: "This card could not be built."}}
}

// dashCSP builds the card. It returns no card when the site already enforces,
// when the environment decides, when an owner dismissed it, or when the
// services are not wired. A nudge never shows an error card.
func (a *Admin) dashCSP(ctx context.Context) (dashResult, error) {
	if a.deps.CSP == nil || a.deps.Site == nil {
		return dashResult{}, nil
	}
	if st := a.deps.CSP.CSPState(); st.Enforcing || st.EnvForced {
		return dashResult{}, nil
	}
	cfg, err := a.deps.Site.Get(ctx)
	if err != nil {
		a.logger().Error("page protection card: site settings unreadable", "err", err)
		return dashResult{}, nil
	}
	if cspPromptHidden(cfg) {
		return dashResult{}, nil
	}
	return dashResult{Sec: &dashSection{ID: "dash-csp", Title: cspCardTitle, State: dashOK,
		CSP: &cspCard{EnforceHref: a.adminPath + cspEnforcePath, HideHref: a.adminPath + cspHidePath, DocsHref: cspDocsURL}}}, nil
}

// handleCSPEnforce switches the public pages to enforcing protection and goes
// back to the Overview.
func (a *Admin) handleCSPEnforce(w http.ResponseWriter, r *http.Request) {
	if a.deps.CSP == nil {
		a.unavailable(w, r, "Page protection")
		return
	}
	if a.deps.CSP.CSPState().EnvForced {
		a.SetFlash(w, r, "error", "The server settings decide this (CSP_ENFORCE).")
		http.Redirect(w, r, a.adminPath+"/", http.StatusSeeOther)
		return
	}
	if err := a.deps.CSP.EnforceCSP(r.Context()); err != nil {
		a.logger().Error("turn on page protection failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "Page protection could not be turned on.")
		return
	}
	a.audit(r, "security.csp_enforce", "site", map[string]any{"enforce": true})
	a.SetFlash(w, r, "success", "Page protection is on.")
	http.Redirect(w, r, a.adminPath+"/", http.StatusSeeOther)
}

// handleCSPHide dismisses the card for the site. It writes one theme_extra key
// and leaves the rest.
func (a *Admin) handleCSPHide(w http.ResponseWriter, r *http.Request) {
	if a.deps.Site == nil {
		a.unavailable(w, r, "Page protection")
		return
	}
	if err := a.deps.Site.PatchThemeExtra(r.Context(), cspHiddenKey, jsontext.Value("true")); err != nil {
		a.logger().Error("hide page protection card failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The card could not be hidden.")
		return
	}
	http.Redirect(w, r, a.adminPath+"/", http.StatusSeeOther)
}
