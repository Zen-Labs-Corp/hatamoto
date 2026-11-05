package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/media"
)

const (
	mediaPerPage  = 24
	pickerPerPage = 12
	// maxUploadFiles is how many files one upload request may carry.
	maxUploadFiles = 20
)

func (a *Admin) mountMedia(authed *httpx.Group) {
	// /media/new, /media/formats and /media/rebuild are literals, so they win
	// over /media/{id}.
	owner := authed.Group("", a.RequireRole("owner"))
	owner.HandleFunc("POST /media/formats", a.handleMediaFormatsSave)
	owner.HandleFunc("POST /media/rebuild", a.handleMediaRebuild)
	authed.HandleFunc("GET /media", a.handleMedia)
	authed.HandleFunc("GET /media/new", a.handleMediaNew)
	authed.HandleFunc("POST /media/upload", a.handleMediaUpload)
	authed.HandleFunc("GET /media/picker", a.handleMediaPicker)
	authed.HandleFunc("POST /media/bulk", a.handleMediaBulk)
	authed.HandleFunc("GET /media/{id}", a.handleMediaDetail)
	authed.HandleFunc("POST /media/{id}", a.handleMediaUpdate)
	authed.HandleFunc("POST /media/{id}/replace", a.handleMediaReplace)
	authed.HandleFunc("GET /media/{id}/delete", a.handleMediaDeleteConfirm)
	authed.HandleFunc("POST /media/{id}/delete", a.handleMediaDelete)
}

// mediaURL is the public URL of the file ("" when unknown).
func mediaURL(m *media.Media) string {
	if m.PublicURL != nil {
		return *m.PublicURL
	}
	return ""
}

// mediaThumb is the small variant's URL, else the file itself for images.
func mediaThumb(m *media.Media) string {
	for _, v := range m.Variants {
		if v.Size == "thumbnail" || v.Size == "medium" {
			return v.URL
		}
	}
	if m.IsImage() {
		return mediaURL(m)
	}
	return ""
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

func mediaMeta(m *media.Media) string {
	meta := humanSize(m.FileSize)
	if m.Width != nil && m.Height != nil {
		meta += fmt.Sprintf(" · %d×%d", *m.Width, *m.Height)
	}
	return meta
}

// mediaExt is the upper-case file extension shown on a tile without a preview.
func mediaExt(m *media.Media) string {
	name := m.OriginalFilename
	if name == "" {
		name = m.Filename
	}
	if e := strings.TrimPrefix(path.Ext(name), "."); e != "" {
		return strings.ToUpper(e)
	}
	_, sub, _ := strings.Cut(m.MimeType, "/")
	return strings.ToUpper(sub)
}

const (
	storageScanPage = 100  // media.Service.List clamps a page to 100
	storageScanCap  = 2000 // files counted for the storage bar
)

// storageBar splits the stored bytes by file type: images, documents, video, other.
func storageBar(items []*media.Media) *ui.CategoryBar {
	var img, doc, vid, other int64
	for _, m := range items {
		switch {
		case strings.HasPrefix(m.MimeType, "image/"):
			img += m.FileSize
		case strings.HasPrefix(m.MimeType, "video/"):
			vid += m.FileSize
		case m.MimeType == "application/pdf":
			doc += m.FileSize
		default:
			other += m.FileSize
		}
	}
	seg := func(label, kind string, n int64) ui.CategorySegment {
		return ui.CategorySegment{Label: label, Value: humanSize(n), N: float64(n), Kind: kind}
	}
	return &ui.CategoryBar{Label: "Storage used by file type", Segments: []ui.CategorySegment{
		seg("Images", "info", img), seg("Documents", "success", doc), seg("Video", "warning", vid), seg("Other", "neutral", other),
	}}
}

// mediaStorage scans the library in pages of 100 (up to storageScanCap files)
// for the storage bar; the service has no aggregate. A failed scan hides the bar.
// It also returns the note and the total size of the files counted.
func (a *Admin) mediaStorage(ctx context.Context, ms MediaStore) (*ui.CategoryBar, string, string) {
	var all []*media.Media
	total := 0
	for len(all) < storageScanCap {
		res, err := ms.List(ctx, &media.ListMediaFilter{}, storageScanPage, len(all))
		if err != nil || len(res.Items) == 0 {
			if err != nil {
				return nil, "", ""
			}
			break
		}
		all = append(all, res.Items...)
		total = res.Total
		if len(all) >= total {
			break
		}
	}
	if len(all) == 0 {
		return nil, "", ""
	}
	var size int64
	for _, m := range all {
		size += m.FileSize
	}
	note := ""
	if total > len(all) {
		note = fmt.Sprintf("Counted from the first %d files.", len(all))
	}
	return storageBar(all), note, humanSize(size)
}

func mimePrefix(kind string) string {
	switch kind {
	case "image":
		return "image/"
	case "video":
		return "video/"
	case "document":
		return "application/pdf"
	}
	return ""
}

type mediaTileView struct {
	ID, URL, Thumb, Filename, Alt, Meta, Href, Ext string
	Decorative, Selectable, Selected, New          bool
}

type mediaListData struct {
	Actions []ui.Button
	Tabs    []ui.Tab
	// FolderTabs is the folder pill row (empty when there are no folders and no
	// folder filter); Folder is the active filter as the URL carries it, and
	// Folders feeds the bulk move select.
	FolderTabs []ui.Tab
	Folder     string
	Folders    []ui.Option
	View       string
	Q, Type    string
	Types      []ui.Option
	Tiles      []mediaTileView
	Table      ui.Table
	Page       ui.Pagination
	Total      int
	MaxMB      int
	Upload     string // action of the upload form
	UpView     string // the view the upload form hands back so the redirect keeps it
	CSRFPath   string
	Back       string // where the bulk delete returns to
	// Storage is the share of space per file type; StorageNote says when only the first files were counted.
	Storage     *ui.CategoryBar
	StorageNote string
	StorageSize string // total size of the files the bar counted
	Formats     *mediaFormatsView
}

// mediaFilter reads the library filter. folder is "" for all files, "/" for
// Uncategorised, else a folder name; a name that breaks the rules is ignored.
func (a *Admin) mediaFilter(r *http.Request) (f *media.ListMediaFilter, q, typ, folder string) {
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	typ = r.URL.Query().Get("type")
	folder = folderParam(r)
	return &media.ListMediaFilter{Search: q, MimeType: mimePrefix(typ), Folder: folder}, q, typ, folder
}

// folderParam is the folder query value in its stored form ("" when absent).
// A value that breaks the naming rules is a legacy stored folder and is kept as given.
func folderParam(r *http.Request) string {
	raw := strings.TrimSpace(r.URL.Query().Get("folder"))
	if raw == "" {
		return ""
	}
	if f, err := media.NormalizeFolder(raw); err == nil {
		return f
	}
	return raw
}

// folderLabel is how a stored folder reads in the admin.
func folderLabel(f string) string {
	if f == media.RootFolder || f == "" {
		return "Uncategorised"
	}
	return strings.TrimPrefix(f, "/")
}

// folderParamValue is the folder in the URL: the name without its slash, "/" for none.
func folderParamValue(f string) string {
	if f == media.RootFolder {
		return f
	}
	return strings.TrimPrefix(f, "/")
}

// mediaFolders lists the folders in use (legacy names that break the naming
// rules included, so they stay filterable and selectable), named folders first
// by name, and the number of files with no folder.
func (a *Admin) mediaFolders(ctx context.Context, ms MediaStore) (named []media.FolderCount, root, total int) {
	all, err := ms.Folders(ctx)
	if err != nil {
		a.logger().Error("media folders failed", "err", err)
		return nil, 0, 0
	}
	for _, f := range all {
		total += f.Count
		if f.Name == media.RootFolder {
			root = f.Count
		} else {
			named = append(named, f)
		}
	}
	slices.SortFunc(named, func(x, y media.FolderCount) int {
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	return named, root, total
}

// folderOptions are the options of a folder select: Uncategorised, then every
// folder; selected is a stored folder ("" selects nothing).
func folderOptions(named []media.FolderCount, selected string, placeholder string) []ui.Option {
	var out []ui.Option
	if placeholder != "" {
		out = append(out, ui.Option{Value: "", Label: placeholder, Selected: selected == ""})
	}
	out = append(out, ui.Option{Value: media.RootFolder, Label: "Uncategorised", Selected: selected == media.RootFolder})
	for _, f := range named {
		out = append(out, ui.Option{Value: folderParamValue(f.Name), Label: folderLabel(f.Name), Selected: strings.EqualFold(f.Name, selected)})
	}
	return out
}

// sameFolder reports whether two folder values name the same folder, ignoring
// case and the leading slash.
func sameFolder(a, b string) bool {
	return strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(a), "/"), strings.TrimPrefix(strings.TrimSpace(b), "/"))
}

// chosenFolder is the folder a move form asks for: the typed new name wins
// over the select. ok is false when neither was given.
func chosenFolder(r *http.Request) (name string, ok bool) {
	if n := strings.TrimSpace(r.PostFormValue("new_folder")); n != "" {
		return n, true
	}
	if f := strings.TrimSpace(r.PostFormValue("folder")); f != "" {
		return f, true
	}
	return "", false
}

func mediaTypeOptions(selected string) []ui.Option {
	var out []ui.Option
	for _, o := range []struct{ v, l string }{{"", "All files"}, {"image", "Images"}, {"video", "Videos"}, {"document", "Documents"}} {
		out = append(out, ui.Option{Value: o.v, Label: o.l, Selected: o.v == selected})
	}
	return out
}

func (a *Admin) handleMedia(w http.ResponseWriter, r *http.Request) {
	ms := a.deps.Content.Media
	if ms == nil {
		a.unavailable(w, r, "Media")
		return
	}
	ctx := r.Context()
	filter, q, typ, folder := a.mediaFilter(r)
	view := r.URL.Query().Get("view")
	if view != "list" {
		view = "grid"
	}
	var folderTabs []ui.Tab
	per := mediaPerPage
	if view == "list" {
		per = 25
	}
	page := pageParam(r)
	res, err := ms.List(ctx, filter, per, (page-1)*per)
	if err != nil {
		a.serverError(w, r, "The media library could not be loaded", err)
		return
	}
	if last := max((res.Total+per-1)/per, 1); page > last {
		page = last
		if res, err = ms.List(ctx, filter, per, (page-1)*per); err != nil {
			a.serverError(w, r, "The media library could not be loaded", err)
			return
		}
	}
	keep := url.Values{}
	if q != "" {
		keep.Set("q", q)
	}
	if typ != "" {
		keep.Set("type", typ)
	}
	named, root, allFiles := a.mediaFolders(ctx, ms)
	folderTab := func(label, value string, count int, active bool) ui.Tab {
		fq := url.Values{}
		for k, vs := range keep {
			fq[k] = vs
		}
		if value != "" {
			fq.Set("folder", value)
		}
		if view == "list" {
			fq.Set("view", "list")
		}
		return ui.Tab{Label: label, Href: a.adminPath + "/media?" + fq.Encode(), Count: count, Active: active}
	}
	if len(named) > 0 || folder != "" {
		d0 := []ui.Tab{folderTab("All files", "", allFiles, folder == "")}
		for _, f := range named {
			d0 = append(d0, folderTab(folderLabel(f.Name), folderParamValue(f.Name), f.Count, strings.EqualFold(f.Name, folder)))
		}
		d0 = append(d0, folderTab("Uncategorised", media.RootFolder, root, folder == media.RootFolder))
		folderTabs = d0
	}
	if folder != "" {
		keep.Set("folder", folder)
		if folder != media.RootFolder {
			keep.Set("folder", folderParamValue(folder))
		}
	}
	created := createdID(r)
	d := mediaListData{Actions: []ui.Button{{Label: "Upload", Icon: "upload", Variant: "primary", Href: a.adminPath + "/media/new", Attrs: map[string]string{"data-hm-open": "upload"}}}, View: view, Q: q, Type: typ, Types: mediaTypeOptions(typ), Total: res.Total, MaxMB: ms.Config().MaxSizeMB,
		Upload: a.adminPath + "/media/upload"}
	d.FolderTabs, d.Folder, d.Folders = folderTabs, folderParamValue(folder), folderOptions(named, "", "Move to")
	if view == "list" {
		d.UpView = view
	}
	for _, v := range []string{"grid", "list"} {
		kq := url.Values{}
		for k, vs := range keep {
			kq[k] = vs
		}
		kq.Set("view", v)
		d.Tabs = append(d.Tabs, ui.Tab{Label: map[string]string{"grid": "Grid", "list": "List"}[v], Href: a.adminPath + "/media?" + kq.Encode(), Active: v == view})
	}
	pq := url.Values{}
	for k, vs := range keep {
		pq[k] = vs
	}
	if view == "list" {
		pq.Set("view", "list")
	}
	d.Back = a.adminPath + "/media?" + pq.Encode()
	// The bar sits outside the swapped results, so search keystrokes and filters never rescan the library.
	if r.Header.Get("HX-Request") != "true" && page == 1 && q == "" && typ == "" && folder == "" {
		d.Storage, d.StorageNote, d.StorageSize = a.mediaStorage(ctx, ms)
	}
	if u := userFrom(ctx); u != nil && u.Role == "owner" {
		d.Formats = a.mediaFormatsView(ctx)
	}
	d.Page = ui.Pagination{Page: page, PerPage: per, Total: res.Total, BaseURL: a.adminPath + "/media", Query: pq}
	loc := a.siteLocation(ctx)
	t := ui.Table{
		ID: "media", Caption: "Media files", BaseURL: a.adminPath + "/media", CSRF: csrfToken(ctx),
		Columns: []ui.Col{{Key: "name", Label: "File"}, {Key: "type", Label: "Type"}, {Key: "size", Label: "Size", Align: "right"}, {Key: "alt", Label: "Alt text"}, {Key: "uploaded", Label: "Uploaded", Align: "right"}},
		Page:    d.Page,
		Empty:   ui.Empty{Icon: "image", Title: "No files match", Body: "Upload a file, or try another search."},
	}
	for _, m := range res.Items {
		id := m.ID.String()
		name := m.OriginalFilename
		if name == "" {
			name = m.Filename
		}
		tile := mediaTileView{ID: id, URL: mediaURL(m), Thumb: mediaThumb(m), Filename: name, Meta: mediaMeta(m), Href: a.adminPath + "/media/" + id, Ext: mediaExt(m), New: page == 1 && id == created}
		if m.AltText != nil {
			tile.Alt = *m.AltText
		}
		if tile.New {
			d.Tiles = append([]mediaTileView{tile}, d.Tiles...) // pinned first, like the table row
		} else {
			d.Tiles = append(d.Tiles, tile)
		}
		alt := ui.Cell{Text: "—"}
		if m.IsImage() {
			alt = ui.Cell{Badge: &ui.Badge{Label: "Missing", Kind: "warning"}}
			if tile.Alt != "" {
				alt = ui.Cell{Text: tile.Alt}
			}
		}
		t.Rows = append(t.Rows, ui.Row{ID: id, Label: name, Cells: []ui.Cell{
			{Text: name, Link: tile.Href}, {Text: m.MimeType}, {Text: humanSize(m.FileSize)}, alt, {Text: fmtDate(m.CreatedAt.In(loc))},
		}, Actions: []ui.Action{
			{Label: "Edit", Href: tile.Href},
			{Label: "Delete", Href: a.adminPath + "/media/" + id + "/delete", Post: a.adminPath + "/media/" + id + "/delete", Danger: true, ConfirmLabel: "Delete file",
				Confirm: "“" + name + "” is deleted from the library. Check where it is used first."},
		}})
	}
	if page == 1 {
		t.Rows = pinFirst(t.Rows, created)
		t.CreatedID = created
	}
	d.Table = t
	v := a.NewView(w, r, "Media")
	v.Data = d
	a.Render(w, r, http.StatusOK, "media/list", v)
}

// handleMediaNew is the no-JavaScript page behind the header Upload button:
// the same dropzone form the list page shows in a dialog.
func (a *Admin) handleMediaNew(w http.ResponseWriter, r *http.Request) {
	ms := a.deps.Content.Media
	if ms == nil {
		a.unavailable(w, r, "Media")
		return
	}
	v := a.NewView(w, r, "Upload files")
	v.Data = mediaListData{MaxMB: ms.Config().MaxSizeMB, Upload: a.adminPath + "/media/upload"}
	a.Render(w, r, http.StatusOK, "media/new", v)
}

// uploadErrorText is the sentence for a rejected upload.
func uploadErrorText(name string, err error, maxMB int) string {
	switch {
	case errors.Is(err, media.ErrFileTooLarge):
		return fmt.Sprintf("%s: the file is too large (the limit is %d MB).", name, maxMB)
	case errors.Is(err, media.ErrQuotaExceeded):
		return name + ": the demo's storage is full. It resets every hour."
	case errors.Is(err, media.ErrImageTooLarge):
		return name + ": the image has too many pixels."
	case errors.Is(err, media.ErrInvalidMimeType):
		return name + ": this file type is not allowed, or the extension does not match the content."
	case errors.Is(err, media.ErrReplaceKind):
		return name + ": replace with the same kind of file."
	case errors.Is(err, media.ErrInvalidFolder):
		return media.ErrInvalidFolder.Error()
	}
	var svgErr *media.SVGError
	if errors.As(err, &svgErr) {
		line := ""
		if svgErr.Line > 0 {
			line = fmt.Sprintf(" (line %d)", svgErr.Line)
		}
		return fmt.Sprintf("%s was not uploaded: %s%s. Remove it and try again.", name, svgErr.Reason, line)
	}
	return name + ": the upload failed."
}

// expectedUploadError is a refusal the user caused and can fix; it is not logged as a failure.
func expectedUploadError(err error) bool {
	var svgErr *media.SVGError
	return errors.Is(err, media.ErrFileTooLarge) || errors.Is(err, media.ErrQuotaExceeded) || errors.Is(err, media.ErrImageTooLarge) ||
		errors.Is(err, media.ErrInvalidMimeType) || errors.Is(err, media.ErrReplaceKind) || errors.Is(err, media.ErrInvalidFolder) || errors.As(err, &svgErr)
}

// uploadedFile is one file of a JSON upload answer.
type uploadedFile struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

func (a *Admin) handleMediaUpload(w http.ResponseWriter, r *http.Request) {
	ms := a.deps.Content.Media
	if ms == nil {
		a.unavailable(w, r, "Media")
		return
	}
	maxBytes := int64(ms.Config().MaxSizeMB) << 20
	// The service enforces the per-file limit; this bounds what one request
	// can spool to disk (not 20 files at the full limit).
	r.Body = http.MaxBytesReader(w, r.Body, min(maxBytes*maxUploadFiles, max(maxBytes*2, 64<<20)))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		a.fail(w, r, http.StatusRequestEntityTooLarge, "That upload is too large or was cut off. Try fewer or smaller files.")
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		a.fail(w, r, http.StatusUnprocessableEntity, "Choose at least one file to upload.")
		return
	}
	if len(files) > maxUploadFiles {
		a.fail(w, r, http.StatusUnprocessableEntity, fmt.Sprintf("Upload at most %d files at a time.", maxUploadFiles))
		return
	}
	done := 0
	var problems []string
	var uploaded []uploadedFile
	var cleanedNotes []string
	for _, h := range files {
		f, err := h.Open()
		if err != nil {
			problems = append(problems, h.Filename+": the file could not be read.")
			continue
		}
		m, err := ms.Upload(r.Context(), actorID(r), f, h.Filename, "", h.Size)
		f.Close()
		if err != nil {
			if !expectedUploadError(err) {
				a.logger().Error("media upload failed", "err", err)
			}
			problems = append(problems, uploadErrorText(h.Filename, err, ms.Config().MaxSizeMB))
			continue
		}
		done++
		uploaded = append(uploaded, uploadedFile{ID: m.ID.String(), URL: mediaURL(m), Name: h.Filename})
		if len(m.Cleaned) > 0 {
			cleanedNotes = append(cleanedNotes, fmt.Sprintf("%s was cleaned: removed %s.", h.Filename, strings.Join(m.Cleaned, ", ")))
			a.audit(r, "media.svg.upload", m.ID.String(), map[string]any{"name": h.Filename, "removed": m.Cleaned})
		}
	}
	if r.URL.Query().Get("json") == "1" {
		// The writing view's paste and drop upload: it shows its own progress
		// and errors, so it gets data, not a redirect.
		status := http.StatusOK
		if done == 0 {
			status = http.StatusUnprocessableEntity
		}
		a.writeJSON(w, status, struct {
			Files  []uploadedFile `json:"files"`
			Errors []string       `json:"errors"`
		}{append([]uploadedFile{}, uploaded...), append([]string{}, problems...)})
		return
	}
	if r.URL.Query().Get("picker") == "1" {
		// The picker dialog stays open and shows the new files first.
		if len(problems) > 0 {
			w.Header().Set("HX-Trigger", `{"hm:toast":{"kind":"error","message":`+jsonString(strings.Join(problems, " "))+`}}`)
		}
		a.renderPicker(w, r, "", "", 1)
		return
	}
	dest := a.adminPath + "/media"
	if r.FormValue("view") == "list" {
		dest += "?view=list" // the user stays in the view they uploaded from
	}
	if len(uploaded) > 0 {
		dest = createdRedirect(dest, uploaded[0].ID)
	}
	switch {
	case len(problems) == 0:
		noun := "file"
		if done != 1 {
			noun = "files"
		}
		a.SetFlash(w, r, "success", strings.TrimSpace(fmt.Sprintf("Uploaded %d %s. %s", done, noun, strings.Join(cleanedNotes, " "))))
	case done == 0:
		a.SetFlash(w, r, "error", strings.Join(problems, " "))
	default:
		a.SetFlash(w, r, "error", strings.TrimSpace(fmt.Sprintf("Uploaded %d. %s %s", done, strings.Join(cleanedNotes, " "), strings.Join(problems, " "))))
	}
	Redirect(w, r, dest)
}

// fail answers a failed action: a flash and redirect for forms, a toast for
// fragment requests.
func (a *Admin) fail(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.URL.Query().Get("json") == "1" {
		a.writeJSON(w, status, map[string]any{"files": []uploadedFile{}, "errors": []string{msg}})
		return
	}
	if IsHTMX(r) {
		w.Header().Set("HX-Reswap", "none")
		w.Header().Set("HX-Trigger", `{"hm:toast":{"kind":"error","message":`+jsonString(msg)+`}}`)
		w.WriteHeader(status)
		return
	}
	a.SetFlash(w, r, "error", msg)
	Redirect(w, r, a.adminPath+"/media")
}

type mediaDetailData struct {
	ID, Name, URL, Filename, Mime, Size, Dims, Uploaded string
	IsImage                                             bool
	Alt, Caption, Credit                                string
	Variants                                            []media.ImageVariant
	Usage                                               []usageView
	UsageErr                                            bool
	Error                                               string
	Crumbs                                              []ui.Crumb
	// Folder is the folder as shown; FolderOptions feeds its select and
	// FolderError is shown under the folder fields.
	Folder        string
	FolderOptions []ui.Option
	FolderError   string
	Replace       string // action of the Replace form
	ReplaceAccept string
}

type usageView struct{ Kind, Name, Href, Type string }

func (a *Admin) usageViews(r *http.Request, m *media.Media) ([]usageView, bool) {
	us, err := a.deps.Content.Media.Usage(r.Context(), m)
	if err != nil {
		a.logger().Error("media usage failed", "err", err)
		return nil, true
	}
	var out []usageView
	for _, u := range us {
		v := usageView{Kind: u.Kind, Name: u.Name, Type: u.SchemaSlug}
		if u.Kind == "entity" {
			v.Href = a.adminPath + "/entities/" + u.ID.String()
		} else {
			v.Type = map[string]string{"site": "Site settings", "menu": "Menu", "plugin": "Plugin settings"}[u.Kind]
		}
		out = append(out, v)
	}
	return out, false
}

func (a *Admin) loadMedia(w http.ResponseWriter, r *http.Request) (*media.Media, bool) {
	ms := a.deps.Content.Media
	id, ok := uuidParam(r, "id")
	if !ok || ms == nil {
		a.notFound(w, r, "File", a.adminPath+"/media", "Back to the library")
		return nil, false
	}
	m, err := ms.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			a.notFound(w, r, "File", a.adminPath+"/media", "Back to the library")
		} else {
			a.serverError(w, r, "The file could not be loaded", err)
		}
		return nil, false
	}
	return m, true
}

func (a *Admin) renderMediaDetail(w http.ResponseWriter, r *http.Request, status int, m *media.Media, alt, caption, credit, errMsg, folderErr string) {
	name := m.OriginalFilename
	if name == "" {
		name = m.Filename
	}
	d := mediaDetailData{ID: m.ID.String(), Name: name, URL: mediaURL(m), Filename: m.Filename, Mime: m.MimeType, Size: humanSize(m.FileSize),
		IsImage: m.IsImage(), Alt: alt, Caption: caption, Credit: credit, Variants: m.Variants, Error: errMsg,
		Uploaded: fmtDateTime(m.CreatedAt.In(a.siteLocation(r.Context()))), Crumbs: []Crumb{{Label: "Media", Href: a.adminPath + "/media"}, {Label: name}},
		Folder: folderLabel(m.Folder), FolderError: folderErr, Replace: a.adminPath + "/media/" + m.ID.String() + "/replace",
		ReplaceAccept: replaceAccept(m)}
	if ms := a.deps.Content.Media; ms != nil {
		named, _, _ := a.mediaFolders(r.Context(), ms)
		d.FolderOptions = folderOptions(named, m.Folder, "")
	}
	if m.Width != nil && m.Height != nil {
		d.Dims = fmt.Sprintf("%d × %d px", *m.Width, *m.Height)
	}
	d.Usage, d.UsageErr = a.usageViews(r, m)
	v := a.NewView(w, r, name)
	v.Data = d
	v.Crumbs = d.Crumbs
	a.Render(w, r, status, "media/detail", v)
}

func (a *Admin) handleMediaDetail(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMedia(w, r)
	if !ok {
		return
	}
	alt, caption, credit := "", "", ""
	if m.AltText != nil {
		alt = *m.AltText
	}
	if m.Caption != nil {
		caption = *m.Caption
	}
	if m.Credit != nil {
		credit = *m.Credit
	}
	a.renderMediaDetail(w, r, http.StatusOK, m, alt, caption, credit, "", "")
}

func (a *Admin) handleMediaUpdate(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMedia(w, r)
	if !ok {
		return
	}
	alt, caption, credit := strings.TrimSpace(r.PostFormValue("alt_text")), strings.TrimSpace(r.PostFormValue("caption")), strings.TrimSpace(r.PostFormValue("credit"))
	if len([]rune(alt)) > 500 || len([]rune(caption)) > 2000 || len([]rune(credit)) > 500 {
		a.renderMediaDetail(w, r, http.StatusUnprocessableEntity, m, alt, caption, credit, "Alt text can be up to 500 characters, the caption up to 2000 and the credit up to 500.", "")
		return
	}
	in := &media.UpdateMediaInput{AltText: &alt, Caption: &caption, Credit: &credit}
	if want, ok := chosenFolder(r); ok {
		// Posting the file's own folder back is no move, even when the stored
		// value is a legacy one that would not pass today's naming rules.
		if !sameFolder(want, m.Folder) {
			in.Folder = &want
		}
	}
	updated, err := a.deps.Content.Media.Update(r.Context(), m.ID, in)
	if err != nil {
		if errors.Is(err, media.ErrInvalidFolder) {
			a.renderMediaDetail(w, r, http.StatusUnprocessableEntity, m, alt, caption, credit, "", err.Error())
			return
		}
		a.serverError(w, r, "The file could not be saved", err)
		return
	}
	if in.Folder != nil && updated != nil && !sameFolder(updated.Folder, m.Folder) {
		a.audit(r, "media.move", "media", map[string]any{"count": 1, "folder": updated.Folder})
	}
	a.SetFlash(w, r, "success", "Saved.")
	Redirect(w, r, a.adminPath+"/media/"+m.ID.String())
}

// replaceAccept is the file types the Replace form offers: the kind of the stored file.
func replaceAccept(m *media.Media) string {
	if exts, ok := media.AllowedExtensionsForMIME(m.MimeType); ok {
		return strings.Join(exts, ",")
	}
	return ""
}

// handleMediaReplace swaps the bytes of a file and keeps its address.
func (a *Admin) handleMediaReplace(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMedia(w, r)
	if !ok {
		return
	}
	ms := a.deps.Content.Media
	maxBytes := int64(ms.Config().MaxSizeMB) << 20
	back := a.adminPath + "/media/" + m.ID.String()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		a.SetFlash(w, r, "error", "That upload is too large or was cut off. Try a smaller file.")
		Redirect(w, r, back)
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		a.SetFlash(w, r, "error", "Choose one file to replace it with.")
		Redirect(w, r, back)
		return
	}
	h := files[0]
	f, err := h.Open()
	if err != nil {
		a.SetFlash(w, r, "error", h.Filename+": the file could not be read.")
		Redirect(w, r, back)
		return
	}
	defer f.Close()
	nm, err := ms.Replace(r.Context(), m.ID, f, h.Filename, h.Size)
	if err != nil {
		if !expectedUploadError(err) {
			a.logger().Error("media replace failed", "err", err)
		}
		a.SetFlash(w, r, "error", uploadErrorText(h.Filename, err, ms.Config().MaxSizeMB))
		Redirect(w, r, back)
		return
	}
	a.audit(r, "media.replace", m.ID.String(), map[string]any{"id": m.ID.String(), "from": m.FileSize, "to": nm.FileSize})
	msg := "Replaced. The address stays the same."
	if len(nm.Cleaned) > 0 {
		msg += fmt.Sprintf(" %s was cleaned: removed %s.", h.Filename, strings.Join(nm.Cleaned, ", "))
		a.audit(r, "media.svg.upload", m.ID.String(), map[string]any{"name": h.Filename, "removed": nm.Cleaned})
	}
	a.SetFlash(w, r, "success", msg)
	Redirect(w, r, back)
}

func (a *Admin) handleMediaDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMedia(w, r)
	if !ok {
		return
	}
	usage, _ := a.usageViews(r, m)
	msg := "The file is deleted from the library and from the server. This cannot be undone."
	if len(usage) > 0 {
		var names []string
		for i, u := range usage {
			if i == 3 {
				names = append(names, "…")
				break
			}
			names = append(names, u.Name)
		}
		msg = fmt.Sprintf("Warning: this file is used in %d %s (%s). They will show a broken image. ", len(usage), plural(len(usage), "place", "places"), strings.Join(names, ", ")) + msg
	}
	name := m.OriginalFilename
	if name == "" {
		name = m.Filename
	}
	a.renderConfirm(w, r, confirmData{Title: "Delete “" + name + "”?", Message: msg, Post: a.adminPath + "/media/" + m.ID.String() + "/delete", Label: "Delete file", Back: a.adminPath + "/media/" + m.ID.String(), Danger: true})
}

func (a *Admin) handleMediaDelete(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMedia(w, r)
	if !ok {
		return
	}
	if err := a.deps.Content.Media.Delete(r.Context(), m.ID); err != nil {
		a.serverError(w, r, "The file could not be deleted", err)
		return
	}
	a.SetFlash(w, r, "success", "File deleted.")
	Redirect(w, r, a.adminPath+"/media")
}

// mediaInUse counts how many of the files are used somewhere. A file that cannot be read or
// checked is skipped: the warning is advice and must not block the confirm page.
func (a *Admin) mediaInUse(r *http.Request, ms MediaStore, ids []string) int {
	used := 0
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		m, err := ms.Get(r.Context(), id)
		if err != nil {
			continue
		}
		if us, err := ms.Usage(r.Context(), m); err == nil && len(us) > 0 {
			used++
		}
	}
	return used
}

// bulkMove puts the ticked files in one folder: the typed new name, else the chosen one.
func (a *Admin) bulkMove(w http.ResponseWriter, r *http.Request, ms MediaStore, ids []string, back string) {
	want, ok := chosenFolder(r)
	if !ok {
		a.SetFlash(w, r, "error", "Choose a folder, or type a new folder name.")
		Redirect(w, r, back)
		return
	}
	// The service checks the name; a stored legacy folder is accepted as it is.
	folder := want
	done, failed := 0, 0
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err == nil {
			var m *media.Media
			if m, err = ms.Update(r.Context(), id, &media.UpdateMediaInput{Folder: &want}); err == nil && m != nil {
				folder = m.Folder
			}
		}
		if errors.Is(err, media.ErrInvalidFolder) {
			a.SetFlash(w, r, "error", err.Error())
			Redirect(w, r, back)
			return
		}
		if err != nil {
			failed++
			continue
		}
		done++
	}
	if done > 0 {
		a.audit(r, "media.move", "media", map[string]any{"count": done, "folder": folder})
	}
	msg := fmt.Sprintf("Moved %d %s to %s.", done, plural(done, "file", "files"), folderLabel(folder))
	if failed > 0 {
		a.SetFlash(w, r, "error", fmt.Sprintf("%s %d could not be moved.", msg, failed))
	} else {
		a.SetFlash(w, r, "success", msg)
	}
	Redirect(w, r, back)
}

// handleMediaBulk deletes or moves the ticked files. The first request answers a
// confirmation page; the confirmed one deletes.
func (a *Admin) handleMediaBulk(w http.ResponseWriter, r *http.Request) {
	ms := a.deps.Content.Media
	if ms == nil {
		a.unavailable(w, r, "Media")
		return
	}
	_ = r.ParseForm()
	back := backTo(r, a.adminPath+"/media", a.adminPath+"/media")
	ids := r.PostForm["id"]
	switch {
	case len(ids) == 0:
		a.SetFlash(w, r, "error", "Select at least one file first.")
		Redirect(w, r, back)
		return
	case len(ids) > 100:
		a.SetFlash(w, r, "error", "Select at most 100 files at a time.")
		Redirect(w, r, back)
		return
	case r.PostFormValue("action") == "move":
		a.bulkMove(w, r, ms, ids, back)
		return
	case r.PostFormValue("action") != "delete":
		a.SetFlash(w, r, "error", "Choose an action for the selected files.")
		Redirect(w, r, back)
		return
	}
	if r.PostFormValue("confirmed") != "1" {
		hidden := []hiddenField{{"action", "delete"}, {"confirmed", "1"}, {"back", back}}
		for _, id := range ids {
			hidden = append(hidden, hiddenField{"id", id})
		}
		msg := "They are deleted from the library and from the server. This cannot be undone."
		if used := a.mediaInUse(r, ms, ids); used > 0 {
			msg = fmt.Sprintf("Warning: %d of these files %s used on your site and will show a broken image. ", used, plural(used, "is", "are")) + msg
		}
		a.renderConfirm(w, r, confirmData{
			Title: fmt.Sprintf("Delete %d %s?", len(ids), plural(len(ids), "file", "files")), Message: msg,
			Post: a.adminPath + "/media/bulk", Label: "Delete files", Back: back, Danger: true, Hidden: hidden,
		})
		return
	}
	done, failed := 0, 0
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err == nil {
			err = ms.Delete(r.Context(), id)
		}
		if err != nil {
			failed++
			continue
		}
		done++
	}
	msg := fmt.Sprintf("Deleted %d %s.", done, plural(done, "file", "files"))
	if failed > 0 {
		a.SetFlash(w, r, "error", fmt.Sprintf("%s %d could not be deleted.", msg, failed))
	} else {
		a.SetFlash(w, r, "success", msg)
	}
	Redirect(w, r, back)
}

type pickerData struct {
	Type       string // "image" (default) or "document"
	Q          string
	Folders    []ui.Option // All folders, Uncategorised and each folder; the active one is selected
	HasFolders bool
	Tiles      []mediaTileView
	Prev, Next string
	Total      int
	MaxMB      int
	Page       int
	Pages      int
}

// pickerType is what the picker lists: images, or PDF documents when a field
// asks for them with ?type=document.
func pickerType(r *http.Request) string {
	if r.URL.Query().Get("type") == "document" {
		return "document"
	}
	return "image"
}

func (a *Admin) handleMediaPicker(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.Media == nil {
		a.unavailable(w, r, "Media")
		return
	}
	a.renderPicker(w, r, strings.TrimSpace(r.URL.Query().Get("q")), folderParam(r), pageParam(r))
}

// renderPicker is the contents of the media picker dialog: images (or PDFs for
// type=document) matching q in folder ("" for every folder), one page at a time. Image fields open it (schemaform's data-hm-media-picker).
func (a *Admin) renderPicker(w http.ResponseWriter, r *http.Request, q, folder string, page int) {
	ms := a.deps.Content.Media
	typ := pickerType(r)
	res, err := ms.List(r.Context(), &media.ListMediaFilter{Search: q, MimeType: mimePrefix(typ), Folder: folder}, pickerPerPage, (page-1)*pickerPerPage)
	if err != nil {
		a.serverError(w, r, "The media library could not be loaded", err)
		return
	}
	named, _, _ := a.mediaFolders(r.Context(), ms)
	d := pickerData{Type: typ, Q: q, Folders: folderOptions(named, folder, "All folders"), HasFolders: len(named) > 0, Total: res.Total, MaxMB: ms.Config().MaxSizeMB, Page: page, Pages: max((res.Total+pickerPerPage-1)/pickerPerPage, 1)}
	for _, m := range res.Items {
		name := m.OriginalFilename
		if name == "" {
			name = m.Filename
		}
		t := mediaTileView{ID: m.ID.String(), URL: mediaURL(m), Thumb: mediaThumb(m), Filename: name, Meta: mediaMeta(m)}
		if m.AltText != nil {
			t.Alt = *m.AltText
		}
		if t.Thumb == "" && !m.IsImage() {
			t.Ext = mediaExt(m) // a file without a preview shows its type
		}
		d.Tiles = append(d.Tiles, t)
	}
	link := func(p int) string {
		nq := url.Values{"page": {strconv.Itoa(p)}}
		if q != "" {
			nq.Set("q", q)
		}
		if typ == "document" {
			nq.Set("type", typ)
		}
		if folder != "" {
			nq.Set("folder", folderParamValue(folder))
		}
		return a.adminPath + "/media/picker?" + nq.Encode()
	}
	if page > 1 {
		d.Prev = link(page - 1)
	}
	if page*pickerPerPage < res.Total {
		d.Next = link(page + 1)
	}
	title := "Choose an image"
	if typ == "document" {
		title = "Choose a document"
	}
	v := a.NewView(w, r, title)
	v.Data = d
	a.Render(w, r, http.StatusOK, "media/picker", v)
}
