package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/httpx"
)

// exportData is the informational Site export & import tab: the site archive
// is made on the command line, so the page only teaches the commands.
type exportData struct {
	ExportCmd, ExportNoSubmissionsCmd, ImportCmd, ResetCmd string
}

func (a *Admin) mountExportSettings(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/export", a.handleExportInfo)
}

func (a *Admin) handleExportInfo(w http.ResponseWriter, r *http.Request) {
	v := a.NewView(w, r, "Site export & import")
	v.Section, v.Tab = "import-export", "export"
	v.Width = "narrow"
	v.Data = exportData{
		ExportCmd:              "hatamoto export site.tar.gz",
		ExportNoSubmissionsCmd: "hatamoto export site.tar.gz --no-submissions",
		ImportCmd:              "DATABASE_URL=postgres://new-host/hatamoto hatamoto import site.tar.gz",
		ResetCmd:               "hatamoto user reset-password --email you@example.com",
	}
	a.Render(w, r, http.StatusOK, "settings/export", v)
}
