package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
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
	// Only the library list moved; the detail, delete, upload and picker
	// endpoints keep their paths so the editor picker is unchanged.
	authed.HandleFunc("GET /settings/media", a.handleMedia)
	authed.HandleFunc("GET /settings/media/new", a.handleMediaNew)
	authed.HandleFunc("POST /media/upload", a.handleMediaUpload)
	authed.HandleFunc("GET /media/picker", a.handleMediaPicker)
	authed.HandleFunc("POST /media/bulk", a.handleMediaBulk)
	authed.HandleFunc("GET /media/{id}", a.handleMediaDetail)
	authed.HandleFunc("POST /media/{id}", a.handleMediaUpdate)
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
	Actions  []ui.Button
	Tabs     []ui.Tab
	View     string
	Q, Type  string
	Types    []ui.Option
	Tiles    []mediaTileView
	Table    ui.Table
	Page     ui.Pagination
	Total    int
	MaxMB    int
	Upload   string // action of the upload form
	UpView   string // the view the upload form hands back so the redirect keeps it
	CSRFPath string
	Back     string // where the bulk delete returns to
	// Storage is the share of space per file type; StorageNote says when only the first files were counted.
	Storage     *ui.CategoryBar
	StorageNote string
	StorageSize string // total size of the files the bar counted
}

func (a *Admin) mediaFilter(r *http.Request) (*media.ListMediaFilter, string, string) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	typ := r.URL.Query().Get("type")
	return &media.ListMediaFilter{Search: q, MimeType: mimePrefix(typ)}, q, typ
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
	filter, q, typ := a.mediaFilter(r)
	view := r.URL.Query().Get("view")
	if view != "list" {
		view = "grid"
	}
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
	created := createdID(r)
	d := mediaListData{Actions: []ui.Button{{Label: "Upload", Icon: "upload", Variant: "primary", Href: a.adminPath + "/settings/media/new", Attrs: map[string]string{"data-hm-open": "upload"}}}, View: view, Q: q, Type: typ, Types: mediaTypeOptions(typ), Total: res.Total, MaxMB: ms.Config().MaxSizeMB,
		Upload: a.adminPath + "/media/upload"}
	if view == "list" {
		d.UpView = view
	}
	for _, v := range []string{"grid", "list"} {
		kq := url.Values{}
		for k, vs := range keep {
			kq[k] = vs
		}
		kq.Set("view", v)
		d.Tabs = append(d.Tabs, ui.Tab{Label: map[string]string{"grid": "Grid", "list": "List"}[v], Href: a.adminPath + "/settings/media?" + kq.Encode(), Active: v == view})
	}
	pq := url.Values{}
	for k, vs := range keep {
		pq[k] = vs
	}
	if view == "list" {
		pq.Set("view", "list")
	}
	d.Back = a.adminPath + "/settings/media?" + pq.Encode()
	// The bar sits outside the swapped results, so search keystrokes and filters never rescan the library.
	if r.Header.Get("HX-Request") != "true" && page == 1 && q == "" && typ == "" {
		d.Storage, d.StorageNote, d.StorageSize = a.mediaStorage(ctx, ms)
	}
	d.Page = ui.Pagination{Page: page, PerPage: per, Total: res.Total, BaseURL: a.adminPath + "/settings/media", Query: pq}
	loc := a.siteLocation(ctx)
	t := ui.Table{
		ID: "media", Caption: "Media files", BaseURL: a.adminPath + "/settings/media", CSRF: csrfToken(ctx),
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
	}
	return name + ": the upload failed."
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
	for _, h := range files {
		f, err := h.Open()
		if err != nil {
			problems = append(problems, h.Filename+": the file could not be read.")
			continue
		}
		m, err := ms.Upload(r.Context(), actorID(r), f, h.Filename, "", h.Size)
		f.Close()
		if err != nil {
			if !errors.Is(err, media.ErrFileTooLarge) && !errors.Is(err, media.ErrQuotaExceeded) && !errors.Is(err, media.ErrImageTooLarge) && !errors.Is(err, media.ErrInvalidMimeType) {
				a.logger().Error("media upload failed", "err", err)
			}
			problems = append(problems, uploadErrorText(h.Filename, err, ms.Config().MaxSizeMB))
			continue
		}
		done++
		uploaded = append(uploaded, uploadedFile{ID: m.ID.String(), URL: mediaURL(m), Name: h.Filename})
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
		a.renderPicker(w, r, "", 1)
		return
	}
	dest := a.adminPath + "/settings/media"
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
		a.SetFlash(w, r, "success", fmt.Sprintf("Uploaded %d %s.", done, noun))
	case done == 0:
		a.SetFlash(w, r, "error", strings.Join(problems, " "))
	default:
		a.SetFlash(w, r, "error", fmt.Sprintf("Uploaded %d. %s", done, strings.Join(problems, " ")))
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
	Redirect(w, r, a.adminPath+"/settings/media")
}

type mediaDetailData struct {
	ID, Name, URL, Filename, Mime, Size, Dims, Uploaded string
	IsImage                                             bool
	Alt, Caption                                        string
	Variants                                            []media.ImageVariant
	Usage                                               []usageView
	UsageErr                                            bool
	Error                                               string
	Crumbs                                              []ui.Crumb
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
		a.notFound(w, r, "File", a.adminPath+"/settings/media", "Back to the library")
		return nil, false
	}
	m, err := ms.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			a.notFound(w, r, "File", a.adminPath+"/settings/media", "Back to the library")
		} else {
			a.serverError(w, r, "The file could not be loaded", err)
		}
		return nil, false
	}
	return m, true
}

func (a *Admin) renderMediaDetail(w http.ResponseWriter, r *http.Request, status int, m *media.Media, alt, caption, errMsg string) {
	name := m.OriginalFilename
	if name == "" {
		name = m.Filename
	}
	d := mediaDetailData{ID: m.ID.String(), Name: name, URL: mediaURL(m), Filename: m.Filename, Mime: m.MimeType, Size: humanSize(m.FileSize),
		IsImage: m.IsImage(), Alt: alt, Caption: caption, Variants: m.Variants, Error: errMsg,
		Uploaded: fmtDateTime(m.CreatedAt.In(a.siteLocation(r.Context()))), Crumbs: a.areaCrumbs("media", Crumb{Label: name})}
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
	alt, caption := "", ""
	if m.AltText != nil {
		alt = *m.AltText
	}
	if m.Caption != nil {
		caption = *m.Caption
	}
	a.renderMediaDetail(w, r, http.StatusOK, m, alt, caption, "")
}

func (a *Admin) handleMediaUpdate(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMedia(w, r)
	if !ok {
		return
	}
	alt, caption := strings.TrimSpace(r.PostFormValue("alt_text")), strings.TrimSpace(r.PostFormValue("caption"))
	if len([]rune(alt)) > 500 || len([]rune(caption)) > 2000 {
		a.renderMediaDetail(w, r, http.StatusUnprocessableEntity, m, alt, caption, "Alt text can be up to 500 characters and the caption up to 2000.")
		return
	}
	if _, err := a.deps.Content.Media.Update(r.Context(), m.ID, &media.UpdateMediaInput{AltText: &alt, Caption: &caption}); err != nil {
		a.serverError(w, r, "The file could not be saved", err)
		return
	}
	a.SetFlash(w, r, "success", "Saved.")
	Redirect(w, r, a.adminPath+"/media/"+m.ID.String())
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
	Redirect(w, r, a.adminPath+"/settings/media")
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

// handleMediaBulk deletes the ticked files. The first request answers a
// confirmation page; the confirmed one deletes.
func (a *Admin) handleMediaBulk(w http.ResponseWriter, r *http.Request) {
	ms := a.deps.Content.Media
	if ms == nil {
		a.unavailable(w, r, "Media")
		return
	}
	_ = r.ParseForm()
	back := backTo(r, a.adminPath+"/settings/media", a.adminPath+"/settings/media")
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
	Q          string
	Tiles      []mediaTileView
	Prev, Next string
	Total      int
	MaxMB      int
	Page       int
	Pages      int
}

func (a *Admin) handleMediaPicker(w http.ResponseWriter, r *http.Request) {
	if a.deps.Content.Media == nil {
		a.unavailable(w, r, "Media")
		return
	}
	a.renderPicker(w, r, strings.TrimSpace(r.URL.Query().Get("q")), pageParam(r))
}

// renderPicker is the contents of the media picker dialog: images matching q,
// one page at a time. Image fields open it (schemaform's data-hm-media-picker).
func (a *Admin) renderPicker(w http.ResponseWriter, r *http.Request, q string, page int) {
	ms := a.deps.Content.Media
	res, err := ms.List(r.Context(), &media.ListMediaFilter{Search: q, MimeType: "image/"}, pickerPerPage, (page-1)*pickerPerPage)
	if err != nil {
		a.serverError(w, r, "The media library could not be loaded", err)
		return
	}
	d := pickerData{Q: q, Total: res.Total, MaxMB: ms.Config().MaxSizeMB, Page: page, Pages: max((res.Total+pickerPerPage-1)/pickerPerPage, 1)}
	for _, m := range res.Items {
		name := m.OriginalFilename
		if name == "" {
			name = m.Filename
		}
		t := mediaTileView{ID: m.ID.String(), URL: mediaURL(m), Thumb: mediaThumb(m), Filename: name, Meta: mediaMeta(m)}
		if m.AltText != nil {
			t.Alt = *m.AltText
		}
		d.Tiles = append(d.Tiles, t)
	}
	link := func(p int) string {
		nq := url.Values{"page": {strconv.Itoa(p)}}
		if q != "" {
			nq.Set("q", q)
		}
		return a.adminPath + "/media/picker?" + nq.Encode()
	}
	if page > 1 {
		d.Prev = link(page - 1)
	}
	if page*pickerPerPage < res.Total {
		d.Next = link(page + 1)
	}
	v := a.NewView(w, r, "Choose an image")
	v.Data = d
	a.Render(w, r, http.StatusOK, "media/picker", v)
}
