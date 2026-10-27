package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// backupsRel is Settings > Backups, relative to the admin prefix.
const backupsRel = "/settings/backups"

// Backup states and kinds as the page sees them. They mirror the engine's
// values; the admin does not import the engine, so the server adapter maps.
const (
	BackupRunning = "running"
	BackupOK      = "ok"
	BackupFailed  = "failed"

	BackupManual    = "manual"
	BackupPreUpdate = "pre_update"
)

var (
	// ErrBackupRunning is returned by Start when a backup is already running.
	ErrBackupRunning = errors.New("a backup is already running")
	// ErrBackupNotFound is returned for an id that is not a backup.
	ErrBackupNotFound = errors.New("backup not found")
)

// BackupInfo is one backup as the page shows it.
type BackupInfo struct {
	ID           string
	Kind         string // BackupManual or BackupPreUpdate
	Target       string // the version a pre-update backup was taken for
	State        string // BackupRunning, BackupOK or BackupFailed
	Percent      int
	Error        string
	Created      time.Time
	Size         int64
	Parts        []string // archive members, e.g. core.dump, content.tar.gz
	Plugins      []string
	MediaMissing int // referenced media files that were not on disk
}

// BackupsState is everything the page needs in one read.
type BackupsState struct {
	Rows          []BackupInfo // newest first
	Running       bool
	FreeBytes     uint64 // free space where backups are written; 0 when unknown
	KeepManual    int
	KeepPreUpdate int
}

// BackupsService is the backup engine as the admin uses it. The server
// adapter implements it; Start never runs the backup on the request context.
type BackupsService interface {
	List(ctx context.Context) (BackupsState, error)
	// Start begins a manual backup in the background and returns its id.
	// It returns ErrBackupRunning when one is already running.
	Start(ctx context.Context, actor string) (string, error)
	// Open returns the archive for download. The caller closes it.
	Open(ctx context.Context, id string) (io.ReadSeekCloser, BackupInfo, error)
	// Delete removes a finished backup; ErrBackupNotFound for an unknown id.
	Delete(ctx context.Context, id string) error
}

type backupsData struct {
	Zone     string
	Keeping  string
	FreeDisk string
	Table    ui.Table
	Running  bool
	Progress ui.Progress // the running backup, shown while Running
	PollURL  string
}

// validBackupID is the shape of a backup id (a UTC time and the kind). Every
// id from a URL passes it before the service sees it.
var validBackupID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-(manual|pre_update|scheduled)$`).MatchString

// SetBackups hands the admin the backup service once the engine is known: the
// server mounts the admin before it exists. Call it before serving starts.
func (a *Admin) SetBackups(svc BackupsService) { a.deps.Backups = svc }

func (a *Admin) mountBackupsSettings(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/backups", a.handleBackups)
	owner.HandleFunc("POST /settings/backups", a.handleBackupStart)
	owner.HandleFunc("GET /settings/backups/{id}/download", a.handleBackupDownload)
	owner.HandleFunc("GET /settings/backups/{id}/delete", a.handleBackupDeleteConfirm)
	owner.HandleFunc("POST /settings/backups/{id}/delete", a.handleBackupDelete)
}

func (a *Admin) handleBackupStart(w http.ResponseWriter, r *http.Request) {
	if a.deps.Backups == nil {
		a.unavailable(w, r, "Backups")
		return
	}
	actor := ""
	if u := userFrom(r.Context()); u != nil {
		actor = u.Email
	}
	id, err := a.deps.Backups.Start(r.Context(), actor)
	switch {
	case errors.Is(err, ErrBackupRunning):
		a.SetFlash(w, r, "warning", "A backup is already running.")
	case err != nil:
		a.logger().Error("backup start", "err", err)
		a.SetFlash(w, r, "error", "The backup could not be started: "+fullStop(err.Error()))
	default:
		a.audit(r, "backups.create", id, map[string]any{"id": id, "kind": BackupManual})
		a.SetFlash(w, r, "info", "Backup started. This page shows its progress.")
	}
	http.Redirect(w, r, a.adminPath+backupsRel, http.StatusSeeOther)
}

// handleBackupDownload streams the archive. It only reads, so a GET is safe;
// the id is validated before it reaches the service.
func (a *Admin) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.deps.Backups == nil {
		a.unavailable(w, r, "Backups")
		return
	}
	if !validBackupID(id) {
		a.backupNotFound(w, r)
		return
	}
	f, info, err := a.deps.Backups.Open(r.Context(), id)
	if errors.Is(err, ErrBackupNotFound) {
		a.backupNotFound(w, r)
		return
	}
	if err != nil {
		a.logger().Error("backup open", "err", err, "id", id)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backup could not be opened.")
		return
	}
	defer f.Close()
	a.audit(r, "backups.download", id, map[string]any{"id": id, "kind": info.Kind})
	h := w.Header()
	h.Set("Content-Type", "application/x-tar")
	h.Set("Content-Disposition", `attachment; filename="`+id+`.tar"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// A backup can take longer to send than the server's write timeout.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		a.logger().Warn("backup download: clear write deadline", "err", err, "id", id)
	}
	http.ServeContent(w, r, id+".tar", info.Created, f)
}

func (a *Admin) backupNotFound(w http.ResponseWriter, r *http.Request) {
	a.Error(w, r, http.StatusNotFound, "Not found", "That backup does not exist.")
}

// backupFor finds the listed backup behind the id in the URL, answering the
// request itself (404, or a flash for a running one) when there is none.
func (a *Admin) backupFor(w http.ResponseWriter, r *http.Request) (BackupInfo, bool) {
	id := r.PathValue("id")
	if a.deps.Backups == nil {
		a.unavailable(w, r, "Backups")
		return BackupInfo{}, false
	}
	if !validBackupID(id) {
		a.backupNotFound(w, r)
		return BackupInfo{}, false
	}
	st, err := a.deps.Backups.List(r.Context())
	if err != nil {
		a.logger().Error("backups list", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backups could not be listed.")
		return BackupInfo{}, false
	}
	for _, b := range st.Rows {
		if b.ID != id {
			continue
		}
		if b.State == BackupRunning {
			a.SetFlash(w, r, "warning", "A running backup cannot be deleted. Delete it when it has finished.")
			http.Redirect(w, r, a.adminPath+backupsRel, http.StatusSeeOther)
			return BackupInfo{}, false
		}
		return b, true
	}
	a.backupNotFound(w, r)
	return BackupInfo{}, false
}

func (a *Admin) handleBackupDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	b, ok := a.backupFor(w, r)
	if !ok {
		return
	}
	a.renderConfirm(w, r, confirmData{
		Title: "Delete this backup?", Label: "Delete backup", Danger: true,
		Message: backupKind(b) + " from " + fmtDateTime(b.Created.In(a.siteLocation(r.Context()))) + " is removed from the server and cannot be recovered.",
		Post:    a.adminPath + backupsRel + "/" + b.ID + "/delete", Back: a.adminPath + backupsRel,
	})
}

func (a *Admin) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.deps.Backups == nil {
		a.unavailable(w, r, "Backups")
		return
	}
	if !validBackupID(id) {
		a.backupNotFound(w, r)
		return
	}
	err := a.deps.Backups.Delete(r.Context(), id)
	switch {
	case errors.Is(err, ErrBackupNotFound):
		a.backupNotFound(w, r)
		return
	case errors.Is(err, ErrBackupRunning):
		a.SetFlash(w, r, "warning", "A running backup cannot be deleted. Delete it when it has finished.")
	case err != nil:
		a.logger().Error("backup delete", "err", err, "id", id)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backup could not be deleted.")
		return
	default:
		a.audit(r, "backups.delete", id, map[string]any{"id": id})
		a.SetFlash(w, r, "success", "Backup deleted.")
	}
	http.Redirect(w, r, a.adminPath+backupsRel, http.StatusSeeOther)
}

func (a *Admin) handleBackups(w http.ResponseWriter, r *http.Request) {
	if a.deps.Backups == nil {
		a.unavailable(w, r, "Backups")
		return
	}
	st, err := a.deps.Backups.List(r.Context())
	if err != nil {
		a.logger().Error("backups list", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The backups could not be listed.")
		return
	}
	// A poll that finds the page in another state moves the whole page on.
	if poll := r.URL.Query().Get("poll"); poll != "" && IsHTMX(r) && poll != backupPollState(st) {
		Redirect(w, r, a.adminPath+backupsRel)
		return
	}
	a.renderBackups(w, r, st)
}

func backupPollState(st BackupsState) string {
	if st.Running {
		return "running"
	}
	return "idle"
}

func (a *Admin) renderBackups(w http.ResponseWriter, r *http.Request, st BackupsState) {
	loc := a.siteLocation(r.Context())
	d := backupsData{
		Zone:    loc.String(),
		Keeping: fmt.Sprintf("Keeping the last %d manual and %d pre-update backups.", st.KeepManual, st.KeepPreUpdate),
		Table:   a.backupsTable(st.Rows, loc),
	}
	if st.FreeBytes > 0 {
		d.FreeDisk = "Free disk: " + backupSize(int64(st.FreeBytes)) + "."
	}
	if st.Running {
		d.Running, d.PollURL = true, a.adminPath+backupsRel+"?poll=running"
		for _, b := range st.Rows {
			if b.State == BackupRunning {
				d.Progress = ui.Progress{Label: "Backup progress", Value: fmt.Sprintf("%d%%", b.Percent), Pct: float64(b.Percent)}
			}
		}
	}
	v := a.NewView(w, r, "Backups")
	if d.Running {
		v.Refresh = pollSeconds
	}
	v.Section, v.Tab = "backups", "backups"
	v.Width = "wide"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/backups", v)
}

// backupSize is a byte count in the largest whole unit, one decimal under 10.
func backupSize(n int64) string {
	units := []string{"bytes", "KB", "MB", "GB", "TB"}
	f, i := float64(n), 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%d bytes", n)
	case f < 10:
		return fmt.Sprintf("%.1f %s", f, units[i])
	}
	return fmt.Sprintf("%.0f %s", f, units[i])
}

func backupKind(b BackupInfo) string {
	if b.Kind == BackupPreUpdate {
		if b.Target != "" {
			return "Before update to " + b.Target
		}
		return "Before an update"
	}
	return "Manual"
}

// backupContains lists what the archive holds, in the owner's words.
func backupContains(b BackupInfo) string {
	var out []string
	has := map[string]bool{}
	for _, p := range b.Parts {
		has[p] = true
	}
	if has["core.dump"] {
		out = append(out, "Database")
	}
	if has["content.tar.gz"] {
		out = append(out, "Pages", "Media")
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ", ")
}

// backupPluginCount is the second line of the Contains cell: how many plugins
// the backup holds, "" for none. The names stay in the sidecar; listing them
// would make the unwrapped column wider than the page.
func backupPluginCount(b BackupInfo) string {
	switch len(b.Plugins) {
	case 0:
		return ""
	case 1:
		return "1 plugin"
	}
	return fmt.Sprintf("%d plugins", len(b.Plugins))
}

// backupSub is the note under the kind (the one wrapping column, so a long
// reason never widens the table): the error of a failed backup,
// or the media gap of one that finished without every file.
func backupSub(b BackupInfo) string {
	if b.State == BackupOK && b.MediaMissing > 0 {
		return fmt.Sprintf("%d media files missing", b.MediaMissing)
	}
	return strings.TrimSpace(b.Error)
}

func backupBadge(b BackupInfo) ui.Badge {
	switch b.State {
	case BackupOK:
		return ui.Badge{Label: "OK", Kind: "success"}
	case BackupRunning:
		return ui.Badge{Label: fmt.Sprintf("Running %d%%", b.Percent), Kind: "info"}
	}
	return ui.Badge{Label: "Failed", Kind: "danger"}
}

func (a *Admin) backupsTable(rows []BackupInfo, loc *time.Location) ui.Table {
	t := ui.Table{
		ID: "backups", Caption: "Backups", BaseURL: a.adminPath + backupsRel,
		Columns: []ui.Col{
			{Key: "date", Label: "Date"}, {Key: "kind", Label: "Kind", Role: "title"},
			{Key: "contains", Label: "Contains", WideOnly: true}, {Key: "size", Label: "Size", Role: "num"},
			{Key: "status", Label: "Status"},
		},
		Empty: ui.Empty{Icon: "database", Title: "No backups yet.", Body: "Back up now takes one."},
	}
	for _, b := range rows {
		badge := backupBadge(b)
		size := "-"
		if b.State == BackupOK {
			size = backupSize(b.Size)
		}
		row := ui.Row{ID: b.ID, Label: backupKind(b) + " " + fmtDateTime(b.Created.In(loc)), Cells: []ui.Cell{
			{Text: fmtDateTime(b.Created.In(loc)), Nowrap: true},
			{Text: backupKind(b), Sub: backupSub(b)},
			{Text: backupContains(b), Sub: backupPluginCount(b)},
			{Text: size, Nowrap: true},
			{Badge: &badge},
		}}
		if b.State == BackupOK {
			row.Actions = append(row.Actions, ui.Action{Label: "Download", Href: a.adminPath + backupsRel + "/" + b.ID + "/download"})
		}
		if b.State != BackupRunning {
			row.Actions = append(row.Actions, ui.Action{Label: "Delete", Href: a.adminPath + backupsRel + "/" + b.ID + "/delete", Post: a.adminPath + backupsRel + "/" + b.ID + "/delete",
				ConfirmLabel: "Delete backup", Danger: true, Confirm: "Delete " + backupKind(b) + " backup? The archive is removed and cannot be recovered."})
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}
