package admin

import (
	"bytes"
	"github.com/google/uuid"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Host functions run as the plugin, with no per-user check, so only the owner
// may call them through the bridge. An editor on a page open to editors can
// still call the plugin's own routes (which enforce their own roles).
func TestBridgeHostCallsAreForTheOwnerOnly(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)

	for _, fn := range []string{"mail.send", "settings.get"} {
		rec := bridge(g, "board", "editor-token", map[string]any{"op": "host", "fn": fn, "args": map[string]any{}})
		if rec.Code != 403 || len(pk.hostCall) != 0 {
			t.Fatalf("editor host call %s: %d %s (host called: %v)", fn, rec.Code, rec.Body.String(), pk.hostCall)
		}
	}
	if rec := bridge(g, "board", "editor-token", map[string]any{"op": "route", "method": "GET", "path": "/leads"}); rec.Code != 200 || len(pk.routeCall) != 1 || !strings.HasSuffix(pk.routeCall[0], "as=editor") {
		t.Errorf("editor route call: %d %v", rec.Code, pk.routeCall)
	}
	if rec := bridge(g, "board", "owner-token", map[string]any{"op": "host", "fn": "settings.get", "args": map[string]any{}}); rec.Code != 200 || len(pk.hostCall) != 1 {
		t.Errorf("owner host call: %d %v", rec.Code, pk.hostCall)
	}

	// The editor's host page does not even list functions.
	body := g.do("GET", "/admin/plugins/board/settings", "editor-token", nil, nil).Body.String()
	if !strings.Contains(body, `data-functions=""`) {
		t.Errorf("an editor's page must carry no host functions:\n%s", body)
	}
	for _, owned := range []string{"hm-tabs", "Turn off", "Turn on", "Permissions"} {
		if strings.Contains(body, owned) {
			t.Errorf("an editor's plugin page must not carry %q", owned)
		}
	}
	if body := g.do("GET", "/admin/plugins/board/settings", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `data-functions="settings.get,mail.send"`) {
		t.Errorf("the owner's page lists the granted functions")
	}
}

func TestBridgeIsRateLimitedPerUserAndPlugin(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	got429 := false
	for i := 0; i < bridgeBurst+20; i++ {
		if rec := bridge(g, "hello", "owner-token", map[string]any{"op": "route", "method": "GET", "path": "/a"}); rec.Code == 429 {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("a tight loop of bridge calls must hit the limit")
	}
	if rec := bridge(g, "board", "owner-token", map[string]any{"op": "route", "method": "GET", "path": "/a"}); rec.Code != 200 {
		t.Errorf("another plugin has its own bucket: %d", rec.Code)
	}
}

// The frame is created by pluginframe.js so its load listener is in place
// first; the server renders it inside a <template>, sandboxed and sourceless.
func TestPluginFrameIsBuiltByTheHostScript(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	body := g.do("GET", "/admin/plugins/hello/settings", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "<template><iframe") || strings.Contains(body, `<iframe class="hm-pluginframe" sandbox="allow-scripts" title="Hello board" referrerpolicy="no-referrer" src=`) {
		t.Errorf("the iframe must sit in a template without a src:\n%s", body)
	}
	if !regexp.MustCompile(`data-hm-pluginframe-notice[^>]*\bhidden\b`).MatchString(body) {
		t.Error("the navigation notice must be there, hidden")
	}
	// A callout is role=note, so a live region around it announces the reveal.
	if !regexp.MustCompile(`<div role="alert"><div class="hm-callout[^>]*data-hm-pluginframe-notice`).MatchString(body) {
		t.Error("the navigation notice must sit in a role=alert live region")
	}
	js := string(readFileT(t, "../../web/admin/js/pluginframe.js"))
	for _, want := range []string{"loads++", "dead = true", "frame.src = src", "m.nonce !== nonce", "e.source !== frame.contentWindow"} {
		if !strings.Contains(js, want) {
			t.Errorf("pluginframe.js lost %q", want)
		}
	}
}

// pluginFrame is what a browser sends when a plugin UI document loads in its iframe.
var pluginFrame = map[string]string{"Sec-Fetch-Dest": "iframe"}

// pluginScript is a sandboxed frame loading its script: an opaque origin, so cross-site.
var pluginScript = map[string]string{"Sec-Fetch-Dest": "script", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Site": "cross-site"}

// pluginDoc is the URL of a plugin UI document with a fresh ticket minted for
// the session behind token, as the plugin page would render it.
func pluginDoc(g *rig, token, plugin, file string) string {
	tok := g.a.tickets.mint(ticketPluginUI, g.sessions.byToken[token].ID, plugin+"/"+file, nil)
	return "/plugin-ui/" + plugin + "/" + file + "?t=" + tok
}

func TestPluginUIDocumentsAreGatedWhateverTheirSuffix(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, f := range []string{"INDEX.HTML", "page.htm", "x.xhtml"} {
		if rec := g.do("GET", "/plugin-ui/hello/"+f, "", nil, pluginFrame); rec.Code != 404 {
			t.Errorf("anonymous %s: %d (a document needs a signed-in user)", f, rec.Code)
		}
		if rec := g.do("GET", pluginDoc(g, "owner-token", "hello", f), "", nil, pluginFrame); rec.Code != 404 {
			t.Errorf("%s with a ticket but no session: %d", f, rec.Code)
		}
	}
	rec := g.do("GET", pluginDoc(g, "owner-token", "hello", "index.html"), "owner-token", nil, pluginFrame)
	h := rec.Header()
	if rec.Code != 200 || h.Get("X-Frame-Options") != "SAMEORIGIN" || h.Get("Content-Security-Policy-Report-Only") != "" || h.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("document headers: %d %v", rec.Code, h)
	}
	if got := g.do("GET", "/plugin-ui/hello/app.js", "", nil, pluginScript).Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("a script is a public static file: Cache-Control %q", got)
	}
}

// ---- upload CSRF ----

func TestUploadTokenTravelsInTheBodyNeverTheURL(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)

	body := g.do("GET", "/admin/plugins", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "_csrf=") {
		t.Error("no form action may carry the token in its query string")
	}
	i := strings.Index(body, `action="/admin/plugins/upload"`)
	if i < 0 {
		t.Fatal("upload form missing")
	}
	form := body[i : i+strings.Index(body[i:], "</form>")]
	if !strings.Contains(form, `name="_csrf"`) || strings.Index(form, `name="_csrf"`) > strings.Index(form, `name="package"`) {
		t.Errorf("the _csrf field must come before the file input:\n%s", form)
	}

	ok := postUpload(g, "/admin/plugins/upload", "owner-token", csrfValue, true, "t.hmp", "theme:dusk")
	if ok.Code != 303 || len(pk.installed) != 1 {
		t.Fatalf("token as the first field: %d", ok.Code)
	}
	// A token in the query string no longer counts, whatever the body holds.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("package", "t.hmp")
	_, _ = fw.Write([]byte("theme:dusk"))
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/admin/plugins/upload?_csrf="+csrfValue, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""}) // the URL token must not count, and no header
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	if rec.Code != 403 || len(pk.installed) != 1 {
		t.Errorf("token in the URL: %d", rec.Code)
	}
	// After the file: refused. Wrong: refused. Missing: refused.
	for name, c := range map[string]*httptest.ResponseRecorder{
		"after the file": postUpload(g, "/admin/plugins/upload", "owner-token", csrfValue, false, "t.hmp", "theme:dusk"),
		"wrong token":    postUpload(g, "/admin/plugins/upload", "owner-token", strings.Repeat("x", 40), true, "t.hmp", "theme:dusk"),
		"no token":       postUpload(g, "/admin/plugins/upload", "owner-token", "", true, "t.hmp", "theme:dusk"),
	} {
		if c.Code != 403 {
			t.Errorf("%s: %d", name, c.Code)
		}
	}
	if len(pk.installed) != 1 {
		t.Errorf("only the valid upload installed something: %+v", pk.installed)
	}
	// The header works too (htmx or fetch clients), with the file part alone.
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("package", "t.hmp")
	_, _ = fw.Write([]byte("theme:dusk"))
	_ = mw.Close()
	req = httptest.NewRequest("POST", "/admin/plugins/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", nil)
	rec = httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	if rec.Code != 303 || len(pk.installed) != 2 {
		t.Errorf("token in the header: %d", rec.Code)
	}
}

// countingBody reports how much of a request body was read.
type countingBody struct {
	r io.Reader
	n int
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestUploadWithABadTokenIsNotBuffered(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_csrf", "wrong")
	fw, _ := mw.CreateFormFile("package", "big.hmp")
	_, _ = fw.Write(bytes.Repeat([]byte("z"), 8<<20))
	_ = mw.Close()
	cb := &countingBody{r: &buf}
	req := httptest.NewRequest("POST", "/admin/plugins/upload", cb)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	g.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	if rec.Code != 403 || cb.n > 64<<10 {
		t.Errorf("status %d after reading %d bytes of an 8 MB body", rec.Code, cb.n)
	}
}

// ---- update confirmation ----

func TestThemeReplacementNeedsAConfirmationStep(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "t.hmp", "theme-old:sunrise")
	body := rec.Body.String()
	if rec.Code != 200 || len(pk.installed) != 0 {
		t.Fatalf("an older, re-signed theme must wait for confirmation: %d installed=%v", rec.Code, pk.installed)
	}
	for _, want := range []string{"Replace theme sunrise?", "not newer than the installed", "2.0.0", "1.0.0", "oldkey", "newkey", "<code>promo</code>", "I understand"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmation page missing %q", want)
		}
	}
	a11yOK(t, body)
	if !strings.Contains(body, `class="hm-confirmcard"`) || !strings.Contains(body, `name="confirm" value="1" required`) {
		t.Error("the replace page is not a confirm card with a required tick")
	}
	id := hiddenValue(t, body, "staged")

	if rec := g.do("POST", "/admin/theme/themes/install", "owner-token", url.Values{"staged": {id}}, nil); rec.Code != 422 || len(pk.installed) != 0 {
		t.Fatalf("without the tick: %d", rec.Code)
	}
	// The failed attempt consumed the stage; start again and confirm.
	body = postMultipart(g, "/admin/plugins/upload", "owner-token", "t.hmp", "theme-old:sunrise").Body.String()
	id = hiddenValue(t, body, "staged")
	if rec := g.do("POST", "/admin/theme/themes/install", "editor-token", url.Values{"staged": {id}, "confirm": {"1"}}, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme/themes/install", "owner-token", url.Values{"staged": {id}, "confirm": {"1"}}, nil); rec.Code != 303 || len(pk.installed) != 1 || !pk.installed[0].confirm {
		t.Fatalf("confirmed: %d %+v", rec.Code, pk.installed)
	}
}

func TestPluginPromptWarnsAndClearsGrantsOnASignerChange(t *testing.T) {
	t.Parallel()

	g, pk, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", "plugin-old:hello")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{"needs your confirmation", "every permission already granted is cleared", `name="confirm"`, "oldkey", "newkey"} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(body, " checked") {
		t.Errorf("a new signer starts with nothing ticked:\n%s", body)
	}
	a11yOK(t, body)
	id := hiddenValue(t, body, "staged")
	if rec := g.do("POST", "/admin/plugins/install", "owner-token", url.Values{"staged": {id}, "permission": {"kv"}, "confirm": {"1"}}, nil); rec.Code != 303 || len(pk.installed) != 1 || !pk.installed[0].confirm {
		t.Fatalf("confirmed install: %d %+v", rec.Code, pk.installed)
	}
}

func TestPluginPromptWarnsAboutAnyAddressAndClearsGrantsWhenAccessWidens(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", "plugin-any:hello")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{"needs your confirmation", "<code>http.fetch.any</code>", "every permission already granted is cleared", "Any web address.", "WARNING: send requests to ANY public web address", "Any public web address (ports 80 and 443)", `name="confirm"`} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(body, " checked") {
		t.Errorf("widened access starts with nothing ticked:\n%s", body)
	}
}

func TestPluginPromptWarnsAboutSiteScripts(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", "plugin-scripts:hello")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	for _, want := range []string{"needs your confirmation", "<code>site.scripts</code>", "runs JavaScript on every public page with full access to the site, including signed-in admins", "JavaScript on every public page.", "Runs its JavaScript on every public page, with full access to the site, including signed-in admins.", `name="confirm"`} {
		if !strings.Contains(body, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(body, " checked") {
		t.Errorf("an update that adds site.scripts needs a fresh tick:\n%s", body)
	}
}

// A first install that asks for site.scripts never starts with it ticked; the
// other permissions keep their default, and the warning is said once.
func TestFreshInstallLeavesSiteScriptsUnticked(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	body := postMultipart(g, "/admin/plugins/upload", "owner-token", "p.hmp", "plugin-fresh-scripts:gtm").Body.String()
	if !strings.Contains(body, `value="kv" checked`) {
		t.Errorf("kv keeps its default tick:\n%s", body)
	}
	if !strings.Contains(body, `value="site.scripts"`) || strings.Contains(body, `value="site.scripts" checked`) {
		t.Errorf("site.scripts must be offered unticked:\n%s", body)
	}
	if n := strings.Count(body, "every public page"); n != 2 { // the label and the help, not the same sentence twice
		t.Errorf("warning text appears %d times", n)
	}
}

func readFileT(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPluginUITickets(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	g.a.tickets.now = clock.now
	owner, editor := g.sessions.byToken["owner-token"].ID, g.sessions.byToken["editor-token"].ID
	doc := "/plugin-ui/hello/index.html?t="
	get := func(token, tok string) int { return g.do("GET", doc+tok, token, nil, pluginFrame).Code }
	mint := func(sess uuid.UUID, target string) string { return g.a.tickets.mint(ticketPluginUI, sess, target, nil) }

	if tok := mint(owner, "hello/index.html"); get("owner-token", tok) != 200 || get("owner-token", tok) != 404 {
		t.Error("a ticket must load once, then 404")
	}
	for name, c := range map[string]struct {
		token, tok string
	}{
		"garbage":             {"owner-token", strings.Repeat("A", 32)},
		"empty":               {"owner-token", ""},
		"no session":          {"", mint(owner, "hello/index.html")},
		"other session":       {"owner-token", mint(editor, "hello/index.html")},
		"other file":          {"owner-token", mint(owner, "hello/other.html")},
		"other plugin":        {"owner-token", mint(owner, "board/index.html")},
		"the preview purpose": {"owner-token", g.a.tickets.mint(ticketPreview, owner, "hello/index.html", nil)},
	} {
		if got := get(c.token, c.tok); got != 404 {
			t.Errorf("%s: %d, want 404", name, got)
		}
	}
	tok := mint(owner, "hello/index.html")
	clock.t = clock.t.Add(ticketTTL + time.Second)
	if got := get("owner-token", tok); got != 404 {
		t.Errorf("expired: %d, want 404", got)
	}
	// Scripts, styles and images need no ticket.
	if rec := g.do("GET", "/plugin-ui/hello/app.js", "", nil, pluginScript); rec.Code != 200 {
		t.Errorf("script: %d", rec.Code)
	}
}

// The plugin frame's confinement is pinned verbatim: a loosening has to show up
// in a diff of this test.
func TestPluginUIConfinementIsPinned(t *testing.T) {
	t.Parallel()

	const csp = "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
	if uiCSP != csp {
		t.Errorf("uiCSP = %q", uiCSP)
	}
	tpl, err := os.ReadFile("../../web/admin/pages/plugins/plugin.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tpl), `sandbox="allow-scripts"`) || strings.Contains(string(tpl), "allow-same-origin") {
		t.Error("the plugin iframe must keep sandbox=\"allow-scripts\" and nothing more")
	}
}
