package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/fonts"
	"hatamoto.dev/core/internal/httpx"
)

// FontService is the font store behind Colours and fonts > Fonts.
type FontService interface {
	List(ctx context.Context) ([]fonts.Font, error)
	Insert(ctx context.Context, fs []fonts.NewFont) error
	DeleteFamily(ctx context.Context, family string) (int, error)
}

// GoogleFonts downloads a font family once from Google Fonts.
type GoogleFonts interface {
	Download(ctx context.Context, family string, weights []int, italic, latinOnly bool) ([]fonts.NewFont, error)
}

const (
	fontsPath         = "/theme/fonts"
	fontsUploadLimit  = fonts.MaxFileBytes + 512<<10 // the file plus the other form fields
	fontsGoogleBudget = 30 * time.Second
)

func (a *Admin) mountFonts(owner *httpx.Group) {
	owner.HandleFunc("GET "+fontsPath, a.handleFonts)
	owner.HandleFunc("GET "+fontsPath+"/upload", a.handleFontUploadForm)
	owner.HandleFunc("POST "+fontsPath+"/upload", a.handleFontUpload)
	owner.HandleFunc("GET "+fontsPath+"/google", a.handleFontGoogleForm)
	owner.HandleFunc("POST "+fontsPath+"/google", a.handleFontGoogle)
	owner.HandleFunc("POST "+fontsPath+"/remove", a.handleFontRemove)
}

// fontsForm is what the two add forms keep when they are shown again.
type fontsForm struct {
	UploadFamily, UploadWeight, UploadStyle string
	GoogleFamily                            string
	GoogleWeights                           map[string]bool
	GoogleItalic, GoogleLatinOnly           bool
}

// fontsData serves the three pages: the list, the Google form and the upload
// form (a list page has no create form on it, so each form has its own page).
type fontsData struct {
	Table         ui.Table
	Actions       []ui.Button
	Form          fontsForm
	WeightOptions []ui.Option
	StyleOptions  []ui.Option
	Weights       []fontWeightChoice
	Errs          map[string]string
	Summary       []ui.FieldError
	Count         int
	Max           int
	Back          string
}

type fontWeightChoice struct {
	ID, Value, Label string
	Checked          bool
}

var fontWeightNames = map[int]string{
	100: "Thin", 200: "Extra light", 300: "Light", 400: "Regular", 500: "Medium",
	600: "Semi bold", 700: "Bold", 800: "Extra bold", 900: "Black",
}

func defaultFontsForm() fontsForm {
	return fontsForm{UploadWeight: "400", UploadStyle: "normal", GoogleWeights: map[string]bool{"400": true, "700": true}, GoogleLatinOnly: true}
}

func (a *Admin) fontService(w http.ResponseWriter, r *http.Request) (FontService, bool) {
	if a.deps.Fonts == nil {
		a.unavailable(w, r, "Fonts")
		return nil, false
	}
	return a.deps.Fonts, true
}

var fontsLabels = []struct{ id, label string }{
	{"upload_file", "Font file"}, {"upload_family", "Family name"}, {"upload_weight", "Weight"}, {"upload_style", "Style"},
	{"google_family", "Google font name"}, {"google_weights", "Weights"}, {"fonts_list", "Your fonts"},
}

// renderFonts shows one of the pages: "list", "google" or "upload".
func (a *Admin) renderFonts(w http.ResponseWriter, r *http.Request, page string, status int, f fontsForm, errs map[string]string) {
	svc, ok := a.fontService(w, r)
	if !ok {
		return
	}
	d := fontsData{Form: f, Errs: errs, Max: fonts.MaxFonts, Back: a.adminPath + fontsPath}
	for _, k := range fontsLabels {
		if m := errs[k.id]; m != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: k.id, Label: k.label, Message: m})
		}
	}
	title, tmpl := "Fonts", "settings/fonts"
	switch page {
	case "list":
		list, err := svc.List(r.Context())
		if err != nil {
			a.logger().Error("list fonts failed", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The fonts could not be loaded.")
			return
		}
		cfg, err := a.loadSite(r.Context())
		if err != nil {
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
			return
		}
		design := designFrom(cfg)
		d.Count = len(list)
		d.Table = a.fontsTable(r, list, fonts.FirstFamily(design.Font), fonts.FirstFamily(design.HeadingFont))
		d.Actions = []ui.Button{
			{Label: "Upload a font", Href: a.adminPath + fontsPath + "/upload"},
			{Label: "Download from Google Fonts", Variant: "primary", Href: a.adminPath + fontsPath + "/google"},
		}
	case "google":
		title, tmpl = "Download from Google Fonts", "settings/fonts-google"
		for w := 100; w <= 900; w += 100 {
			v := strconv.Itoa(w)
			d.Weights = append(d.Weights, fontWeightChoice{ID: "google_weight_" + v, Value: v, Label: fmt.Sprintf("%d %s", w, fontWeightNames[w]), Checked: f.GoogleWeights[v]})
		}
	case "upload":
		title, tmpl = "Upload a font", "settings/fonts-upload"
		for w := 100; w <= 900; w += 100 {
			v := strconv.Itoa(w)
			d.WeightOptions = append(d.WeightOptions, ui.Option{Value: v, Label: fmt.Sprintf("%d %s", w, fontWeightNames[w]), Selected: v == f.UploadWeight})
		}
		d.StyleOptions = []ui.Option{
			{Value: "normal", Label: "Normal", Selected: f.UploadStyle != "italic"},
			{Value: "italic", Label: "Italic", Selected: f.UploadStyle == "italic"},
		}
	}
	v := a.NewView(w, r, title)
	v.Section, v.Tab = "colours", "fonts"
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, tmpl, v)
}

var fontRowSlug = regexp.MustCompile(`[^a-z0-9]+`)

// fontFamilyRow is one family with its faces summed up.
type fontFamilyRow struct {
	name    string
	weights map[int]bool
	italic  bool
	sources map[string]bool
}

func (a *Admin) fontsTable(r *http.Request, list []fonts.Font, body, heading string) ui.Table {
	byFamily := map[string]*fontFamilyRow{}
	var order []string
	for _, f := range list {
		k := strings.ToLower(f.Family)
		row := byFamily[k]
		if row == nil {
			row = &fontFamilyRow{name: f.Family, weights: map[int]bool{}, sources: map[string]bool{}}
			byFamily[k] = row
			order = append(order, k)
		}
		row.weights[f.Weight] = true
		row.italic = row.italic || f.Style == "italic"
		row.sources[f.Source] = true
	}
	sort.Strings(order)
	t := ui.Table{
		ID: "fonts", Caption: "Your fonts", BaseURL: a.adminPath + fontsPath, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "family", Label: "Family", Role: "title"}, {Key: "faces", Label: "Weights and styles"}, {Key: "source", Label: "Source"}, {Key: "used", Label: "Used by"}},
		Empty:   ui.Empty{Icon: "file-text", Title: "No fonts yet", Body: "Upload a .woff2 file or download a font from Google Fonts below. The pages of your site then use it from your own server."},
	}
	for _, k := range order {
		row := byFamily[k]
		var ws []string
		for w := 100; w <= 900; w += 100 {
			if row.weights[w] {
				ws = append(ws, strconv.Itoa(w))
			}
		}
		faces := strings.Join(ws, ", ")
		if row.italic {
			faces += " (with italic)"
		}
		var src []string
		if row.sources["upload"] {
			src = append(src, "Uploaded")
		}
		if row.sources["google"] {
			src = append(src, "Google Fonts")
		}
		var used []string
		if strings.EqualFold(row.name, body) {
			used = append(used, "Body")
		}
		if strings.EqualFold(row.name, heading) {
			used = append(used, "Headings")
		}
		usedText := strings.Join(used, " and ")
		if usedText == "" {
			usedText = "Not used"
		}
		t.Rows = append(t.Rows, ui.Row{ID: "font-" + fontRowSlug.ReplaceAllString(k, "-"), Label: row.name, Cells: []ui.Cell{
			{Text: row.name}, {Text: faces}, {Text: strings.Join(src, " and ")}, {Text: usedText},
		}, Actions: []ui.Action{{
			Label: "Remove", Post: a.adminPath + fontsPath + "/remove", Fields: map[string]string{"family": row.name},
			Danger: true, Button: true, AriaLabel: "Remove " + row.name,
		}}})
	}
	return t
}

func (a *Admin) handleFonts(w http.ResponseWriter, r *http.Request) {
	a.renderFonts(w, r, "list", http.StatusOK, defaultFontsForm(), nil)
}

func (a *Admin) handleFontUploadForm(w http.ResponseWriter, r *http.Request) {
	a.renderFonts(w, r, "upload", http.StatusOK, defaultFontsForm(), nil)
}

func (a *Admin) handleFontGoogleForm(w http.ResponseWriter, r *http.Request) {
	a.renderFonts(w, r, "google", http.StatusOK, defaultFontsForm(), nil)
}

// fontChange finishes a successful write: audit, drop cached pages, flash and
// go back to the list.
func (a *Admin) fontChange(w http.ResponseWriter, r *http.Request, action, family, message string, detail map[string]any) {
	a.audit(r, action, family, detail)
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", message)
	http.Redirect(w, r, a.adminPath+fontsPath, http.StatusSeeOther)
}

// fontUserError turns a store or validation error into the owner's wording
// ("" for an error the owner cannot act on).
func (a *Admin) fontUserError(err error) string {
	switch {
	case errors.Is(err, fonts.ErrTooMany):
		return fmt.Sprintf("You can keep at most %d font files. Remove a font first.", fonts.MaxFonts)
	case errors.Is(err, fonts.ErrNotWOFF2), errors.Is(err, fonts.ErrTooBig), errors.Is(err, fonts.ErrBadFamily),
		errors.Is(err, fonts.ErrBadWeight), errors.Is(err, fonts.ErrBadStyle):
		return err.Error()
	}
	return ""
}

func (a *Admin) handleFontUpload(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.fontService(w, r)
	if !ok {
		return
	}
	f := defaultFontsForm()
	r.Body = http.MaxBytesReader(w, r.Body, fontsUploadLimit)
	if err := r.ParseMultipartForm(fonts.MaxFileBytes); err != nil {
		var tooBig *http.MaxBytesError
		msg := "The upload could not be read. Try again."
		if errors.As(err, &tooBig) {
			msg = fonts.ErrTooBig.Error()
		}
		a.renderFonts(w, r, "upload", http.StatusUnprocessableEntity, f, map[string]string{"upload_file": msg})
		return
	}
	f.UploadFamily = strings.TrimSpace(r.PostFormValue("family"))
	f.UploadWeight, f.UploadStyle = r.PostFormValue("weight"), r.PostFormValue("style")
	fail := func(id, msg string) {
		a.renderFonts(w, r, "upload", http.StatusUnprocessableEntity, f, map[string]string{id: msg})
	}
	weight, _ := strconv.Atoi(f.UploadWeight)
	file, _, err := r.FormFile("file")
	if err != nil {
		fail("upload_file", "Choose a .woff2 file")
		return
	}
	defer func() { _ = file.Close() }()
	data, hash, err := fonts.ValidateUpload(f.UploadFamily, weight, f.UploadStyle, file)
	if err != nil {
		id := map[error]string{
			fonts.ErrBadFamily: "upload_family", fonts.ErrBadWeight: "upload_weight", fonts.ErrBadStyle: "upload_style",
		}[err]
		if id == "" {
			id = "upload_file"
		}
		msg := a.fontUserError(err)
		if msg == "" {
			a.logger().Error("read font upload failed", "err", err)
			msg = "The file could not be read. Try again."
		}
		fail(id, msg)
		return
	}
	list, err := svc.List(r.Context())
	if err != nil {
		a.logger().Error("list fonts failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The fonts could not be loaded.")
		return
	}
	uploads := 0
	for _, e := range list {
		if e.Source == "upload" {
			uploads++
		}
		if strings.EqualFold(e.Family, f.UploadFamily) && e.Weight == weight && e.Style == f.UploadStyle {
			fail("upload_family", fmt.Sprintf("%s %d %s is already installed. Remove the family first to replace it.", e.Family, weight, f.UploadStyle))
			return
		}
	}
	if uploads >= fonts.MaxUploads {
		fail("upload_file", fmt.Sprintf("You can upload at most %d font files. Remove one first.", fonts.MaxUploads))
		return
	}
	if err := svc.Insert(r.Context(), []fonts.NewFont{{
		Family: f.UploadFamily, Weight: weight, Style: f.UploadStyle, Source: "upload", Hash: hash, Data: data,
	}}); err != nil {
		if msg := a.fontUserError(err); msg != "" {
			fail("upload_file", msg)
			return
		}
		a.logger().Error("store font failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The font could not be saved.")
		return
	}
	a.fontChange(w, r, "fonts.upload", f.UploadFamily, f.UploadFamily+" added.",
		map[string]any{"family": f.UploadFamily, "weight": weight, "style": f.UploadStyle, "bytes": len(data)})
}

func (a *Admin) handleFontGoogle(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.fontService(w, r)
	if !ok {
		return
	}
	if a.deps.FontsGoogle == nil {
		a.unavailable(w, r, "Downloading from Google Fonts")
		return
	}
	f := defaultFontsForm()
	f.GoogleFamily = strings.TrimSpace(r.PostFormValue("family"))
	f.GoogleWeights = map[string]bool{}
	var weights []int
	for _, s := range r.PostForm["weights"] {
		if w, err := strconv.Atoi(s); err == nil && fonts.ValidWeight(w) && !f.GoogleWeights[s] {
			f.GoogleWeights[s] = true
			weights = append(weights, w)
		}
	}
	f.GoogleItalic = r.PostFormValue("italic") != ""
	f.GoogleLatinOnly = r.PostFormValue("latin_only") != ""
	fail := func(id, msg string) {
		a.renderFonts(w, r, "google", http.StatusUnprocessableEntity, f, map[string]string{id: msg})
	}
	if !fonts.ValidFamily(f.GoogleFamily) {
		fail("google_family", fonts.ErrBadFamily.Error())
		return
	}
	if len(weights) == 0 {
		fail("google_weights", "Choose at least one weight")
		return
	}
	list, err := svc.List(r.Context())
	if err != nil {
		a.logger().Error("list fonts failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The fonts could not be loaded.")
		return
	}
	for _, e := range list {
		if strings.EqualFold(e.Family, f.GoogleFamily) {
			fail("google_family", e.Family+" is already installed. Remove it first to download it again.")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), fontsGoogleBudget)
	defer cancel()
	sort.Ints(weights)
	got, err := a.deps.FontsGoogle.Download(ctx, f.GoogleFamily, weights, f.GoogleItalic, f.GoogleLatinOnly)
	if err != nil {
		a.logger().Warn("google font download failed", "family", f.GoogleFamily, "err", err)
		fail("google_family", "The font could not be downloaded: "+err.Error())
		return
	}
	if err := svc.Insert(ctx, got); err != nil {
		if msg := a.fontUserError(err); msg != "" {
			fail("google_family", msg)
			return
		}
		a.logger().Error("store font failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The font could not be saved.")
		return
	}
	a.fontChange(w, r, "fonts.google", f.GoogleFamily, f.GoogleFamily+" downloaded and added.",
		map[string]any{"family": f.GoogleFamily, "weights": weights, "italic": f.GoogleItalic, "latin_only": f.GoogleLatinOnly, "files": len(got)})
}

func (a *Admin) handleFontRemove(w http.ResponseWriter, r *http.Request) {
	svc, ok := a.fontService(w, r)
	if !ok {
		return
	}
	family := strings.TrimSpace(r.PostFormValue("family"))
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	design := designFrom(cfg)
	for _, u := range []struct{ stack, role string }{{design.Font, "body"}, {design.HeadingFont, "heading"}} {
		if strings.EqualFold(fonts.FirstFamily(u.stack), family) {
			a.renderFonts(w, r, "list", http.StatusUnprocessableEntity, defaultFontsForm(), map[string]string{
				"fonts_list": fmt.Sprintf("%s is the %s font. Choose another font first.", family, u.role),
			})
			return
		}
	}
	n, err := svc.DeleteFamily(r.Context(), family)
	if err != nil {
		a.logger().Error("remove font failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The font could not be removed.")
		return
	}
	if n == 0 {
		a.SetFlash(w, r, "info", family+" was already gone.")
		http.Redirect(w, r, a.adminPath+fontsPath, http.StatusSeeOther)
		return
	}
	a.fontChange(w, r, "fonts.remove", family, family+" removed.", map[string]any{"family": family, "files": n})
}
