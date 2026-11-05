package admin

import (
	"fmt"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/render"
)

const additionalCSSPath = "/theme/additional-css"

type additionalCSSData struct {
	Summary []ui.FieldError
	Err     string
	CSS     string
	// Size reads "1.2 KB of 64 KB".
	Size string
}

func (a *Admin) handleAdditionalCSS(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	a.renderAdditionalCSS(w, r, http.StatusOK, render.AdditionalCSS(cfg.ThemeExtra), nil)
}

func (a *Admin) handleAdditionalCSSSave(w http.ResponseWriter, r *http.Request) {
	if a.deps.Site == nil {
		a.unavailable(w, r, "Additional CSS")
		return
	}
	_ = r.ParseForm() // a malformed body is empty text, which clears the CSS
	css := strings.ReplaceAll(r.PostForm.Get("additional_css"), "\r\n", "\n")
	if issue := render.ValidateCustomCSS(css); issue != nil {
		a.renderAdditionalCSS(w, r, http.StatusUnprocessableEntity, css, issue)
		return
	}
	if strings.TrimSpace(css) == "" {
		css = ""
	}
	raw, err := jsonx.Marshal(css, jsonx.HTMLSafe)
	if err != nil {
		a.logger().Error("encode additional css failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The CSS could not be saved.")
		return
	}
	if err := a.deps.Site.PatchThemeExtra(r.Context(), render.AdditionalCSSKey, raw); err != nil {
		a.logger().Error("save additional css failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The CSS could not be saved.")
		return
	}
	a.audit(r, "theme.additional_css", "Additional CSS", map[string]any{"bytes": len(css)})
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Additional CSS saved.")
	http.Redirect(w, r, a.adminPath+additionalCSSPath, http.StatusSeeOther)
}

func (a *Admin) renderAdditionalCSS(w http.ResponseWriter, r *http.Request, status int, css string, issue *render.CSSIssue) {
	d := additionalCSSData{
		CSS:  css,
		Size: fmt.Sprintf("%.1f KB of %d KB", float64(len(css))/1024, render.MaxCustomCSS>>10),
	}
	if issue != nil {
		d.Err = issue.Error()
		d.Summary = []ui.FieldError{{ID: "additional-css", Label: "Additional CSS", Message: d.Err}}
	}
	v := a.NewView(w, r, "Additional CSS")
	v.Section, v.Tab = "additional-css", "additional-css"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/additional-css", v)
}
