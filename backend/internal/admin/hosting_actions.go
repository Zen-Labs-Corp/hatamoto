package admin

import (
	"errors"
	"net/http"
	"strings"
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

// handleHostingTest starts a Test export of the saved target. The service runs
// it in the background; the run row in the log is the feedback.
func (a *Admin) handleHostingTest(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Hosting
	if svc == nil {
		a.unavailable(w, r, "Hosting")
		return
	}
	if v, err := svc.View(r.Context()); err == nil && v.Mode == HostingStatic {
		a.SetFlash(w, r, "info", hostingTestStaticWhy)
		http.Redirect(w, r, a.adminPath+hostingPath, http.StatusSeeOther)
		return
	}
	err := svc.TestExport(r.Context())
	switch {
	case errors.Is(err, ErrHostingNoTarget):
		a.SetFlash(w, r, "warning", "Save a target first.")
	case errors.Is(err, ErrHostingTestBusy):
		a.SetFlash(w, r, "info", "An export is running. Try the test export again when it finishes.")
	case err != nil:
		a.logger().Error("hosting test export", "err", err)
		a.SetFlash(w, r, "error", "The test export could not be started: "+fullStop(err.Error()))
	default:
		a.audit(r, "hosting.test_export", "hosting", nil)
		a.SetFlash(w, r, "success", "Test export started. It writes the files to the saved target and shows in the log as Test export.")
	}
	http.Redirect(w, r, a.adminPath+hostingPath, http.StatusSeeOther)
}

// handleHostingHostKeyConfirm reads the host key the saved SFTP host presents
// and asks the owner to trust it. It writes nothing; the fingerprint is the
// only hidden field of the form.
func (a *Admin) handleHostingHostKeyConfirm(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Hosting
	if svc == nil {
		a.unavailable(w, r, "Hosting")
		return
	}
	back := a.adminPath + hostingPath
	fp, err := svc.ProbeHostKey(r.Context())
	switch {
	case errors.Is(err, ErrHostingNoTarget):
		a.SetFlash(w, r, "warning", "Save an SFTP target first.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	case err != nil:
		a.SetFlash(w, r, "error", "Could not read the host key: "+fullStop(err.Error()))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	v, err := svc.View(r.Context())
	if err != nil {
		a.logger().Error("hosting settings", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The hosting settings could not be loaded.")
		return
	}
	a.renderConfirm(w, r, confirmData{
		Post: back + "/host-key", Back: back, Hidden: []hiddenField{{Name: "host_key", Value: fp}},
		Title: "Trust this host key?", Label: "Trust host key",
		Message: v.SFTPHost + ":" + v.SFTPPort + " presented the key " + fp + ". Compare it with the fingerprint your hosting provider shows before you trust it. Exports stop if the key changes.",
	})
}

// handleHostingTrustHostKey pins the shown fingerprint. The service checks the
// host still presents it.
func (a *Admin) handleHostingTrustHostKey(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Hosting
	if svc == nil {
		a.unavailable(w, r, "Hosting")
		return
	}
	back := a.adminPath + hostingPath
	fp := strings.TrimSpace(r.PostFormValue("host_key"))
	if !strings.HasPrefix(fp, "SHA256:") {
		a.SetFlash(w, r, "error", "The host key was not trusted: no fingerprint was sent.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	switch err := svc.TrustHostKey(r.Context(), fp); {
	case err == nil:
		a.audit(r, "hosting.trust_host_key", "hosting", map[string]any{"fingerprint": fp})
		a.SetFlash(w, r, "success", "Host key trusted.")
	case errors.Is(err, ErrHostingHostKeyChanged):
		a.SetFlash(w, r, "warning", err.Error())
	default:
		a.SetFlash(w, r, "error", "The host key was not trusted: "+fullStop(err.Error()))
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
