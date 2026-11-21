package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/internal/adminpagestate"
)

type exampleData struct {
	SaveURL, Name string
	Danger        adminpagestate.DangerConfirm
}

func exampleDanger(c adminpagestate.Context) adminpagestate.DangerConfirm {
	return adminpagestate.DangerConfirm{
		ID: "erase", Title: "Erase example data", Phrase: "erase", Prompt: "Type erase to confirm",
		Action: c.URL("erase"), Back: c.URL(), Label: "Erase", CSRF: c.CSRF(),
	}
}

func examplePage() adminpagestate.Page {
	get := func(c adminpagestate.Context) error {
		return c.Render(200, "example", "Example module", exampleData{SaveURL: c.URL("save"), Name: c.User().Name})
	}
	return adminpagestate.Page{
		ID: "example", Label: "Example", Desc: "An example module page.", Icon: "puzzle",
		Templates: os.DirFS("../../adminpages/testdata/example"),
		Routes: []adminpagestate.Route{
			{Method: "GET", Handle: get},
			{Method: "POST", Path: "/save", Handle: func(c adminpagestate.Context) error {
				c.Flash("success", "Example saved.")
				c.Redirect("")
				return nil
			}},
			{Method: "GET", Path: "/erase", Sudo: true, Handle: func(c adminpagestate.Context) error {
				return c.Render(200, "confirm", "Erase", exampleData{Danger: exampleDanger(c)})
			}},
			{Method: "POST", Path: "/erase", Sudo: true, Handle: func(c adminpagestate.Context) error {
				dc, ok := c.CheckDanger(exampleDanger(c))
				if !ok {
					return c.Render(422, "confirm", "Erase", exampleData{Danger: dc})
				}
				c.Redirect("")
				return nil
			}},
		},
	}
}

// failingPage has one route per way a handler can fail.
func failingPage() adminpagestate.Page {
	route := func(path string, h adminpagestate.HandlerFunc) adminpagestate.Route {
		return adminpagestate.Route{Method: "GET", Path: path, Handle: h}
	}
	return adminpagestate.Page{
		ID: "failing", Label: "Failing", Templates: fstest.MapFS{"pages/broken.html": {Data: []byte("{{if}}")}, "pages/exec.html": {Data: []byte(`{{template "layouts/shell" .}}{{define "main"}}{{.Data.Missing.Field}}{{end}}`)}},
		Routes: []adminpagestate.Route{
			route("", func(adminpagestate.Context) error { return nil }),
			route("/err", func(adminpagestate.Context) error { return errors.New("boom") }),
			route("/panic", func(adminpagestate.Context) error { panic("boom") }),
			route("/parse", func(c adminpagestate.Context) error { return c.Render(200, "broken", "x", nil) }),
			route("/exec", func(c adminpagestate.Context) error { return c.Render(200, "exec", "x", struct{}{}) }),
			route("/unknown", func(c adminpagestate.Context) error { return c.Render(200, "nope", "x", nil) }),
		},
	}
}

func newModuleRig(t *testing.T) *rig {
	t.Helper()
	return newSudoRig(t, func(d *Deps) { d.ModulePages = []adminpagestate.Page{examplePage(), failingPage()} })
}

const exampleBase = "/admin/settings/modules/example"

func TestModulePageOwnerSeesShellAndSubnav(t *testing.T) {
	t.Parallel()
	g := newModuleRig(t)
	rec := g.do("GET", exampleBase, "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"Example module page", "hm-shell", "hm-page", `href="` + exampleBase + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
}

func TestModulePageEditorForbidden(t *testing.T) {
	t.Parallel()
	g := newModuleRig(t)
	for _, c := range []struct{ method, path string }{{"GET", ""}, {"POST", "/save"}, {"GET", "/erase"}, {"POST", "/erase"}} {
		var form url.Values
		if c.method == "POST" {
			form = url.Values{}
		}
		if rec := g.do(c.method, exampleBase+c.path, "editor-token", form, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status %d", c.method, c.path, rec.Code)
		}
	}
}

func TestModulePagePostNeedsCSRFToken(t *testing.T) {
	t.Parallel()
	g := newModuleRig(t)
	g.realTokens()
	if rec := g.do("POST", exampleBase+"/save", "owner-token", url.Values{}, map[string]string{"X-CSRF-Token": ""}); rec.Code != http.StatusForbidden {
		t.Fatalf("without token: %d", rec.Code)
	}
	rec := g.browserForm(exampleBase+"/save", "owner-token", "", url.Values{}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != exampleBase {
		t.Fatalf("with token: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	req := httptest.NewRequest("GET", exampleBase, nil)
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	setFetchMeta(req, csrfValue)
	next := httptest.NewRecorder()
	g.h.ServeHTTP(next, req)
	if !strings.Contains(next.Body.String(), "Example saved.") {
		t.Fatalf("flash not shown: %d", next.Code)
	}
}

func TestModulePageSudoAndDangerConfirm(t *testing.T) {
	t.Parallel()
	g := newModuleRig(t)
	rec := g.do("GET", exampleBase+"/erase", "owner-token", nil, nil)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "sudo") {
		t.Fatalf("unelevated: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	g.elevate("owner-token")
	if rec := g.do("GET", exampleBase+"/erase", "owner-token", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("elevated GET: %d", rec.Code)
	}
	rec = g.do("POST", exampleBase+"/erase", "owner-token", url.Values{"confirm_phrase": {"wrong"}}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), dangerMismatch) {
		t.Fatalf("wrong phrase: %d", rec.Code)
	}
	rec = g.do("POST", exampleBase+"/erase", "owner-token", url.Values{"confirm_phrase": {"erase"}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("right phrase: %d", rec.Code)
	}
}

func TestModulePageFailsClosed(t *testing.T) {
	t.Parallel()
	g := newModuleRig(t)
	for _, path := range []string{"/err", "/panic", "/parse", "/exec", "/unknown"} {
		rec := g.do("GET", "/admin/settings/modules/failing"+path, "owner-token", nil, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusInternalServerError || !strings.Contains(body, "Something went wrong") || !strings.Contains(body, "hm-shell") {
			t.Errorf("%s: status %d, body %.200q", path, rec.Code, body)
		}
	}
}

func TestModulePagesAbsentWithoutModules(t *testing.T) {
	t.Parallel()
	g := newRig(t)
	if rec := g.do("GET", exampleBase, "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}
