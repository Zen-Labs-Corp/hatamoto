package admin

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"hatamoto.dev/core/internal/fonts"
	"hatamoto.dev/core/internal/jsonx"
)

// fakeFonts is an in-memory FontService.
type fakeFonts struct {
	mu   sync.Mutex
	rows []fonts.Font
}

func (f *fakeFonts) List(context.Context) ([]fonts.Font, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fonts.Font(nil), f.rows...), nil
}

func (f *fakeFonts) Insert(_ context.Context, in []fonts.NewFont) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.rows)+len(in) > fonts.MaxFonts {
		return fonts.ErrTooMany
	}
	for _, n := range in {
		f.rows = append(f.rows, fonts.Font{Family: n.Family, Weight: n.Weight, Style: n.Style, Source: n.Source, Subset: n.Subset, Hash: n.Hash, Size: len(n.Data)})
	}
	return nil
}

func (f *fakeFonts) DeleteFamily(_ context.Context, family string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []fonts.Font
	n := 0
	for _, r := range f.rows {
		if strings.EqualFold(r.Family, family) {
			n++
		} else {
			keep = append(keep, r)
		}
	}
	f.rows = keep
	return n, nil
}

func (f *fakeFonts) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.rows) }

// fakeGoogle is a GoogleFonts that records its call.
type fakeGoogle struct {
	err               error
	family            string
	weights           []int
	italic, latinOnly bool
	calls             int
}

func (g *fakeGoogle) Download(_ context.Context, family string, weights []int, italic, latinOnly bool) ([]fonts.NewFont, error) {
	g.calls++
	g.family, g.weights, g.italic, g.latinOnly = family, weights, italic, latinOnly
	if g.err != nil {
		return nil, g.err
	}
	var out []fonts.NewFont
	for _, w := range weights {
		out = append(out, fonts.NewFont{Family: family, Weight: w, Style: "normal", Source: "google", Subset: "latin", Hash: strings.Repeat("a", 64), Data: []byte("wOF2x")})
	}
	return out, nil
}

type fontsRig struct {
	*rig
	site    *fakeSite
	fonts   *fakeFonts
	google  *fakeGoogle
	events  *[]AuditEvent
	flushed *int
}

func newFontsRig(t *testing.T) *fontsRig {
	t.Helper()
	site := newFakeSite("Acme Site")
	ff, gg := &fakeFonts{}, &fakeGoogle{}
	var events []AuditEvent
	flushed := 0
	g := newRig(t, func(d *Deps) {
		d.Site = themeSite{site}
		d.Fonts, d.FontsGoogle = ff, gg
		d.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
		d.Invalidate = func(context.Context) { flushed++ }
	})
	return &fontsRig{rig: g, site: site, fonts: ff, google: gg, events: &events, flushed: &flushed}
}

func (g *fontsRig) install(family string, weight int, source string) {
	g.fonts.rows = append(g.fonts.rows, fonts.Font{Family: family, Weight: weight, Style: "normal", Source: source, Subset: "latin", Hash: strings.Repeat("b", 64), Size: 1234})
}

func (g *fontsRig) useFonts(body, heading string) {
	raw, _ := jsonx.Marshal(map[string]any{"typography": map[string]string{"fontFamily": body, "headingFamily": heading}})
	g.site.cfg.ThemeExtra = raw
}

func (g *fontsRig) lastAudit(t *testing.T) AuditEvent {
	t.Helper()
	if len(*g.events) == 0 {
		t.Fatal("no audit entry written")
	}
	return (*g.events)[len(*g.events)-1]
}

func woff2(n int) []byte { return append([]byte("wOF2"), make([]byte, n)...) }

// postFont posts the upload form the way the page does: _csrf is the first part.
func (g *fontsRig) postFont(token string, fields map[string]string, file []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_csrf", csrfValue)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "brand.woff2")
		_, _ = fw.Write(file)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/admin/theme/fonts/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	g.addFetchMeta(req, token, map[string]string{"X-CSRF-Token": ""})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func goodUpload() map[string]string {
	return map[string]string{"family": "Brand Sans", "weight": "700", "style": "italic"}
}

func TestFontsPagesGoldenAndA11y(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	for path, name := range map[string]string{
		"/admin/theme/fonts":        "pages/settings-fonts-empty.html",
		"/admin/theme/fonts/google": "pages/settings-fonts-google.html",
		"/admin/theme/fonts/upload": "pages/settings-fonts-upload.html",
	} {
		rec := g.do("GET", path, "owner-token", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		a11yOK(t, rec.Body.String())
		compareGolden(t, name, normalizePage(rec.Body.String()))
	}

	g.install("Brand Sans", 400, "upload")
	g.install("Brand Sans", 700, "google")
	g.useFonts(`"Brand Sans", sans-serif`, "Inter, sans-serif")
	rec := g.do("GET", "/admin/theme/fonts", "owner-token", nil, nil)
	body := rec.Body.String()
	a11yOK(t, body)
	compareGolden(t, "pages/settings-fonts.html", normalizePage(body))
	for _, want := range []string{
		"Brand Sans", "400, 700", "Uploaded and Google Fonts", "Body",
		`action="/admin/theme/fonts/remove"`, `aria-label="Remove Brand Sans"`, `name="family" value="Brand Sans"`,
		`href="/admin/theme/fonts/upload"`, `href="/admin/theme/fonts/google"`, "2 of 60 font files",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(body, "<script src=\"https://") || strings.Contains(body, "googleapis") {
		t.Error("the page loads something from outside")
	}
}

func TestFontsOwnerOnlyAndUnavailable(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	for _, p := range []string{"/admin/theme/fonts", "/admin/theme/fonts/upload", "/admin/theme/fonts/google"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d", p, rec.Code)
		}
	}
	if rec := g.do("POST", "/admin/theme/fonts/remove", "editor-token", url.Values{"family": {"X"}}, nil); rec.Code != 403 {
		t.Errorf("editor remove = %d", rec.Code)
	}
	none := newRig(t, func(d *Deps) { d.Site = newFakeSite("Acme") })
	if rec := none.do("GET", "/admin/theme/fonts", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("no font service = %d, want 503", rec.Code)
	}
}

func TestFontUploadStoresAndAudits(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	rec := g.postFont("owner-token", goodUpload(), woff2(64))
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/theme/fonts" {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
	}
	if g.fonts.count() != 1 || g.fonts.rows[0].Weight != 700 || g.fonts.rows[0].Style != "italic" || g.fonts.rows[0].Source != "upload" {
		t.Errorf("stored = %+v", g.fonts.rows)
	}
	if e := g.lastAudit(t); e.Action != "fonts.upload" || e.Target != "Brand Sans" {
		t.Errorf("audit = %+v", e)
	}
	if *g.flushed != 1 {
		t.Errorf("page cache flushed %d times, want 1", *g.flushed)
	}
}

func TestFontUploadRefusals(t *testing.T) {
	t.Parallel()
	with := func(k, v string) map[string]string {
		m := goodUpload()
		m[k] = v
		return m
	}
	tests := []struct {
		name   string
		fields map[string]string
		file   []byte
		want   string
	}{
		{"not woff2", goodUpload(), []byte("OTTO and not a woff2 font"), "not a .woff2 font"},
		{"oversize", goodUpload(), woff2(fonts.MaxFileBytes), "at most 2 MB"},
		{"weight 450", with("weight", "450"), woff2(8), "weight must be"},
		{"style", with("style", "oblique"), woff2(8), "style must be"},
		{"family", with("family", `Bad"Name`), woff2(8), "family name can use"},
		{"no file", goodUpload(), nil, "Choose a .woff2 file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newFontsRig(t)
			rec := g.postFont("owner-token", tc.fields, tc.file)
			if rec.Code != 422 || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d, want 422 with %q", rec.Code, tc.want)
			}
			if g.fonts.count() != 0 || len(*g.events) != 0 || *g.flushed != 0 {
				t.Error("a refused upload changed something")
			}
		})
	}
}

func TestFontUploadLimitsAndDuplicates(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	g.install("Brand Sans", 700, "upload")
	g.fonts.rows[0].Style = "italic"
	rec := g.postFont("owner-token", goodUpload(), woff2(8))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "already installed") {
		t.Errorf("duplicate = %d", rec.Code)
	}

	g = newFontsRig(t)
	for i := 0; i < fonts.MaxUploads; i++ {
		g.install("Many", 100, "upload")
	}
	rec = g.postFont("owner-token", goodUpload(), woff2(8))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "at most 20 font files") || g.fonts.count() != fonts.MaxUploads {
		t.Errorf("21st upload = %d", rec.Code)
	}

	g = newFontsRig(t)
	for i := 0; i < fonts.MaxFonts; i++ {
		g.install("Google Many", 100, "google")
	}
	rec = g.postFont("owner-token", goodUpload(), woff2(8))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "at most 60 font files") {
		t.Errorf("61st file = %d", rec.Code)
	}
}

func TestFontGoogleDownload(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	form := url.Values{"family": {"Open Sans"}, "weights": {"700", "400", "450", "x"}, "italic": {"true"}, "latin_only": {"true"}}
	rec := g.do("POST", "/admin/theme/fonts/google", "owner-token", form, nil)
	if rec.Code != 303 {
		t.Fatalf("google = %d %s", rec.Code, rec.Body.String())
	}
	if g.google.family != "Open Sans" || len(g.google.weights) != 2 || g.google.weights[0] != 400 || g.google.weights[1] != 700 || !g.google.italic || !g.google.latinOnly {
		t.Errorf("asked %q %v italic %v latin %v", g.google.family, g.google.weights, g.google.italic, g.google.latinOnly)
	}
	if g.fonts.count() != 2 {
		t.Errorf("stored %d faces", g.fonts.count())
	}
	if e := g.lastAudit(t); e.Action != "fonts.google" || e.Target != "Open Sans" || e.Detail["files"] != 2 {
		t.Errorf("audit = %+v", e)
	}
	if *g.flushed != 1 {
		t.Errorf("flushed %d", *g.flushed)
	}

	// Latin only is a checkbox: absent means off.
	g.fonts.rows = nil
	g.do("POST", "/admin/theme/fonts/google", "owner-token", url.Values{"family": {"Lora"}, "weights": {"400"}}, nil)
	if g.google.latinOnly || g.google.italic {
		t.Errorf("unticked boxes read as on: italic %v latin %v", g.google.italic, g.google.latinOnly)
	}
}

func TestFontGoogleRefusals(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	post := func(form url.Values) *httptest.ResponseRecorder {
		return g.do("POST", "/admin/theme/fonts/google", "owner-token", form, nil)
	}
	if rec := post(url.Values{"family": {`Bad"Name`}, "weights": {"400"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "family name can use") {
		t.Errorf("bad family = %d", rec.Code)
	}
	if rec := post(url.Values{"family": {"Lora"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "at least one weight") {
		t.Errorf("no weight = %d", rec.Code)
	}
	g.install("Lora", 400, "google")
	if rec := post(url.Values{"family": {"lora"}, "weights": {"400"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "already installed") {
		t.Errorf("installed = %d", rec.Code)
	}
	g.fonts.rows = nil
	g.google.err = errors.New("Google Fonts could not be reached")
	if rec := post(url.Values{"family": {"Lora"}, "weights": {"400"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "could not be downloaded") {
		t.Errorf("download error = %d", rec.Code)
	}
	if g.fonts.count() != 0 || len(*g.events) != 0 || *g.flushed != 0 {
		t.Error("a failed download changed something")
	}
}

func TestFontRemove(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	g.install("Brand Sans", 400, "upload")
	g.install("Brand Sans", 700, "upload")
	g.install("Display", 400, "upload")
	g.useFonts(`"Brand Sans", sans-serif`, `"display", serif`)
	remove := func(family string) *httptest.ResponseRecorder {
		return g.do("POST", "/admin/theme/fonts/remove", "owner-token", url.Values{"family": {family}}, nil)
	}
	if rec := remove("Brand Sans"); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Brand Sans is the body font. Choose another font first.") {
		t.Errorf("body font removed: %d", rec.Code)
	}
	if rec := remove("Display"); rec.Code != 422 || !strings.Contains(rec.Body.String(), "is the heading font") {
		t.Errorf("heading font removed: %d", rec.Code)
	}
	if g.fonts.count() != 3 || len(*g.events) != 0 || *g.flushed != 0 {
		t.Fatal("a refused removal changed something")
	}

	g.useFonts("Inter, sans-serif", "Inter, sans-serif")
	if rec := remove("Brand Sans"); rec.Code != 303 {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body.String())
	}
	if g.fonts.count() != 1 {
		t.Errorf("%d faces left, want 1", g.fonts.count())
	}
	if e := g.lastAudit(t); e.Action != "fonts.remove" || e.Target != "Brand Sans" || e.Detail["files"] != 2 {
		t.Errorf("audit = %+v", e)
	}
	if *g.flushed != 1 {
		t.Errorf("flushed %d", *g.flushed)
	}
}

func TestDesignSaveWithFontChoice(t *testing.T) {
	t.Parallel()
	g := newFontsRig(t)
	g.install("Brand Sans", 400, "upload")
	rec := g.do("GET", "/admin/theme/colours", "owner-token", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{`name="font_choice"`, `name="heading_font_choice"`, `value="&#34;Brand Sans&#34;, sans-serif"`, "Custom stack"} {
		if !strings.Contains(body, want) {
			t.Errorf("colours page lacks %q", want)
		}
	}
	a11yOK(t, body)

	form := func() url.Values {
		return url.Values{"primary": {"#112233"}, "secondary": {"#445566"}, "accent": {"#778899"}, "background": {"#ffffff"}, "text": {"#000000"},
			"font": {"Inter, sans-serif"}, "heading_font": {"Georgia, serif"}, "radius_sm": {"2px"}, "radius_md": {"6px"}, "radius_lg": {"1rem"}}
	}
	typography := func() map[string]string {
		var got struct{ Typography map[string]string }
		_ = jsonx.Unmarshal(g.site.cfg.ThemeExtra, &got, jsonx.StoredLoose)
		return got.Typography
	}

	f := form()
	f.Set("font_choice", `"Brand Sans", sans-serif`)
	f.Set("heading_font_choice", "custom")
	if rec := g.do("POST", "/admin/theme/colours", "owner-token", f, nil); rec.Code != 303 {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	if ty := typography(); ty["fontFamily"] != `"Brand Sans", sans-serif` || ty["headingFamily"] != "Georgia, serif" {
		t.Errorf("stored typography = %v", ty)
	}
	if e := g.lastAudit(t); e.Action != "design.fonts" {
		t.Errorf("audit = %+v", e)
	}
	n := len(*g.events)

	// The same values again change nothing, so no font audit entry.
	f = form()
	f.Set("font", `"Brand Sans", sans-serif`)
	f.Set("heading_font", "Georgia, serif")
	g.do("POST", "/admin/theme/colours", "owner-token", f, nil)
	if len(*g.events) != n {
		t.Errorf("unchanged fonts wrote an audit entry")
	}

	// A choice that is not an installed family is ignored.
	f = form()
	f.Set("font_choice", `"Not Installed", sans-serif`)
	g.do("POST", "/admin/theme/colours", "owner-token", f, nil)
	if ty := typography(); ty["fontFamily"] != "Inter, sans-serif" {
		t.Errorf("an unknown choice was stored: %v", ty)
	}

	// The builder drawer posts font only, in fetch mode; the choice is not part of it.
	f = url.Values{"font": {"Georgia, serif"}, "font_choice": {`"Brand Sans", sans-serif`}}
	if rec := g.do("POST", "/admin/theme/colours", "owner-token", f, map[string]string{"X-Hm-Fetch": "1"}); rec.Code != 204 {
		t.Fatalf("fetch save = %d %s", rec.Code, rec.Body.String())
	}
	if ty := typography(); ty["fontFamily"] != "Georgia, serif" {
		t.Errorf("fetch mode read the choice: %v", ty)
	}
}
