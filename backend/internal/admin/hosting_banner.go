package admin

import "net/http"

// ExportBanner is the owner's notice that the last static export failed.
type ExportBanner struct{ Message, Href string }

// exportBanner is the banner for a page view: nil unless a signed-in owner is
// looking at the dashboard or the Hosting page and the newest finished export
// failed. It reads the last run on those two pages only; a failed read shows
// no banner.
func (a *Admin) exportBanner(r *http.Request, u *CurrentUser) *ExportBanner {
	if a.deps.Hosting == nil || u == nil || u.Role != "owner" {
		return nil
	}
	p := r.URL.Path
	if p != a.adminPath && p != a.adminPath+"/" && p != a.adminPath+hostingPath {
		return nil
	}
	run, ok, err := a.deps.Hosting.LastRun(r.Context())
	if err != nil || !ok || run.Result != HostingRunFailed {
		return nil
	}
	when := fmtDateTime(run.Started.In(a.siteLocation(r.Context())))
	msg := "Last export failed " + when + ": " + endSentence(clip(run.FirstError, 200)) + " Your previous export is still live."
	return &ExportBanner{Message: msg, Href: a.adminPath + hostingPath}
}
