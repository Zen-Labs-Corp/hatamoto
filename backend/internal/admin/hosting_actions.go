package admin

import (
	"errors"
	"net/http"
)

// handleHostingModeConfirm is the page between Save and the sudo-gated switch.
// Its form posts the mode and nothing else, never a secret.
func (a *Admin) handleHostingModeConfirm(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	d := confirmData{Post: a.adminPath + hostingPath + "/mode", Back: a.adminPath + hostingPath, Hidden: []hiddenField{{Name: "mode", Value: to}}}
	switch to {
	case HostingStatic:
		d.Title, d.Label = "Switch to static files?", "Switch to static files"
		d.Message = "Pages will be published as files to your target and only islands will call this server. The first export starts now."
	case HostingServed:
		d.Title, d.Label = "Serve pages from Hatamoto again?", "Switch to served"
		d.Message = "Exports stop. Files already published stay where they are."
	default:
		http.Redirect(w, r, a.adminPath+hostingPath, http.StatusSeeOther)
		return
	}
	a.renderConfirm(w, r, d)
}

func (a *Admin) handleHostingMode(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Hosting
	if svc == nil {
		a.unavailable(w, r, "Hosting")
		return
	}
	back := a.adminPath + hostingPath
	mode := r.PostFormValue("mode")
	if mode != HostingServed && mode != HostingStatic {
		a.SetFlash(w, r, "error", "Choose how pages are served.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if err := svc.SetMode(r.Context(), mode); err != nil {
		a.SetFlash(w, r, "error", "The mode was not changed: "+fullStop(err.Error()))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	a.audit(r, "hosting.mode", "hosting", map[string]any{"mode": mode})
	if mode == HostingServed {
		a.SetFlash(w, r, "success", "Pages are served by Hatamoto again.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	switch err := svc.Export(r.Context(), true, "mode"); {
	case err == nil, errors.Is(err, ErrHostingExportQueued):
		a.SetFlash(w, r, "success", "Static mode is on. The first export has started.")
	default:
		a.logger().Error("hosting first export", "err", err)
		a.SetFlash(w, r, "warning", "Static mode is on, but the first export could not start: "+fullStop(err.Error()))
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (a *Admin) handleHostingExport(w http.ResponseWriter, r *http.Request) {
	a.startHostingExport(w, r, false, "manual")
}

func (a *Admin) handleHostingRebuild(w http.ResponseWriter, r *http.Request) {
	a.startHostingExport(w, r, true, "rebuild")
}

// startHostingExport asks the service to start a run. It only enqueues: the
// export itself never runs on the request.
func (a *Admin) startHostingExport(w http.ResponseWriter, r *http.Request, full bool, trigger string) {
	svc := a.deps.Hosting
	if svc == nil {
		a.unavailable(w, r, "Hosting")
		return
	}
	err := svc.Export(r.Context(), full, trigger)
	switch {
	case errors.Is(err, ErrHostingNotStatic):
		a.SetFlash(w, r, "error", fullStop(err.Error()))
	case errors.Is(err, ErrHostingExportQueued):
		a.SetFlash(w, r, "info", fullStop(err.Error()))
	case err != nil:
		a.logger().Error("hosting export", "err", err)
		a.SetFlash(w, r, "error", "The export could not be started: "+fullStop(err.Error()))
	default:
		a.audit(r, "hosting.export", "hosting", map[string]any{"full": full})
		msg := "Export started."
		if full {
			msg = "Full rebuild started."
		}
		a.SetFlash(w, r, "success", msg)
	}
	http.Redirect(w, r, a.adminPath+hostingPath, http.StatusSeeOther)
}
