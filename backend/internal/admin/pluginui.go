package admin

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/time/rate"

	"hatamoto.dev/core/internal/httpx"
)

// Plugin admin pages. A plugin that declares ui in its manifest ships a page in
// its package's ui/ directory. The page runs in <iframe sandbox="allow-scripts">
// (no allow-same-origin: an opaque origin, no cookies, no admin DOM), is served
// from /plugin-ui/<plugin>/ with a strict CSP (no network of its own), and
// reaches the host only through a postMessage bridge the admin page relays:
//
//	iframe --postMessage--> pluginframe.js --POST /admin/plugins/<p>/bridge--> host
//
// The bridge endpoint runs as the signed-in user (a role the plugin's ui.roles
// allows) and offers two operations:
//
//   - route: call one of the plugin's own routes (the same registry as
//     /api/v1/p/<plugin>, with each route's role check). Every role the page is
//     open to may do this.
//   - host: call a host function the plugin's granted permissions allow. The
//     owner granted those permissions to the plugin's code, not to everyone who
//     can open its page, and a host call carries no per-user check, so this is
//     for the owner only. An editor's page reaches the same data through the
//     plugin's own routes, which carry per-route roles and the plugin's checks.

const (
	uiCSP = "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
	maxBridgeBody = 1 << 20
)

// mountPluginUIRoot registers the path the iframe loads from, outside /admin so
// the admin's own headers never apply to plugin code.
func (a *Admin) mountPluginUIRoot(r *httpx.Group) {
	r.HandleFunc("GET /plugin-ui/_sdk/bridge.js", a.handlePluginSDK)
	r.HandleFunc("GET /plugin-ui/{plugin}/{file...}", a.handlePluginUIFile)
}

// mountPluginUIScreens registers the old /ui redirect and the bridge endpoint.
func (a *Admin) mountPluginUIScreens(authed *httpx.Group) {
	g := authed.Group("", a.RequireRole("owner", "editor"))
	g.HandleFunc("GET /plugins/{name}/ui", a.handlePluginUIRedirect)
	g.HandleFunc("POST /plugins/{name}/bridge", a.handlePluginBridge)
}

func uiRoles(u *PluginUIInfo) []string {
	if len(u.Roles) == 0 {
		return []string{"owner"}
	}
	return u.Roles
}

// uiAllows reports whether p's UI is loaded and open to role.
func uiAllows(p PluginInfo, role string) bool {
	return p.Loaded && p.UI != nil && slices.Contains(uiRoles(p.UI), role)
}

// firstUIPlugin is the name of the first plugin whose UI role may open, or "".
func (a *Admin) firstUIPlugin(role string) string {
	pa := a.deps.Growth.Plugins
	if pa == nil {
		return ""
	}
	for _, p := range pa.Plugins() {
		if uiAllows(p, role) {
			return p.Name
		}
	}
	return ""
}

// uiPlugin finds a loaded plugin with a page the role may open.
func (a *Admin) uiPlugin(name, role string) (PluginInfo, bool) {
	pa := a.deps.Growth.Plugins
	if pa == nil {
		return PluginInfo{}, false
	}
	for _, p := range pa.Plugins() {
		if p.Name == name && uiAllows(p, role) {
			return p, true
		}
	}
	return PluginInfo{}, false
}

type pluginUIData struct {
	Name, Title, Src, Bridge, Functions string
}

// pluginUIFor is the plugin's admin UI frame for role, or nil when it has none,
// role may not open it, or the request has no session to bind the frame to. The
// frame URL carries a one-time ticket (frameticket.go) for the signed-in
// session of r.
func (a *Admin) pluginUIFor(r *http.Request, p PluginInfo, role string) *pluginUIData {
	if !uiAllows(p, role) {
		return nil
	}
	entry := p.UI.Entry
	if entry == "" {
		entry = "index.html"
	}
	_, sess, ok := a.cachedAuth(r)
	if !ok || sess == nil {
		return nil
	}
	tok := a.tickets.mint(ticketPluginUI, sess.ID, p.Name+"/"+entry, nil)
	if tok == "" {
		return nil
	}
	d := &pluginUIData{Name: p.Name, Title: p.UI.Title, Src: "/plugin-ui/" + p.Name + "/" + entry + "?t=" + tok, Bridge: a.adminPath + "/plugins/" + p.Name + "/bridge"}
	if role == "owner" { // host functions are the owner's (see handlePluginBridge)
		d.Functions = strings.Join(p.UI.Functions, ",")
	}
	return d
}

// handlePluginUIRedirect keeps the old /ui address working: the UI now lives on
// the plugin's Settings tab.
func (a *Admin) handlePluginUIRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, a.pluginURL(r.PathValue("name"), "settings"), http.StatusMovedPermanently)
}

// handlePluginUIFile serves ui/<file> of the plugin's package. An HTML document
// needs a signed-in user the plugin's page is open to and the one-time ticket
// (?t=) that page minted for this session and file; every miss is the admin
// refusal. Scripts, styles and images are public static files (a sandboxed
// frame sends no cookies for them).
func (a *Admin) handlePluginUIFile(w http.ResponseWriter, r *http.Request) {
	pk := a.deps.Growth.Packages
	plugin, file := r.PathValue("plugin"), r.PathValue("file")
	if pk == nil {
		http.NotFound(w, r)
		return
	}
	if isUIDocument(file) {
		u, sess, ok := a.authenticate(r)
		if !ok || sess == nil {
			a.refuse(w, r)
			return
		}
		if _, ok := a.uiPlugin(plugin, string(u.Role)); !ok {
			a.refuse(w, r)
			return
		}
		if _, ok := a.tickets.consume(ticketPluginUI, r.URL.Query().Get("t"), sess.ID, plugin+"/"+file); !ok {
			a.refuse(w, r)
			return
		}
	}
	h := w.Header()
	h.Set("Vary", "Cookie")
	// The frame is embedded by the admin page (frame-ancestors 'self' in the
	// CSP); say so for browsers and proxies that read X-Frame-Options, and drop
	// the site-wide report-only policy that would flag the embedding.
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Del("Content-Security-Policy-Report-Only")
	cache := "no-cache"
	if isUIDocument(file) {
		cache = "private, no-cache"
	}
	if !pk.UIFile(w, r, plugin, "ui/"+file, uiCSP, cache) {
		http.NotFound(w, r)
	}
}

// isUIDocument reports whether a ui/ file is an HTML or XHTML document (by
// extension, whatever its case, or by its resolved content type): those need a
// signed-in user. Scripts, styles and images stay public: a sandboxed frame
// sends no cookies for them (so an SVG used as an image must stay public too).
func isUIDocument(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".html", ".htm", ".xhtml", ".xht":
		return true
	}
	ct := mime.TypeByExtension(path.Ext(file))
	return strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml")
}

// handlePluginSDK serves the small client a plugin page includes to talk to the
// bridge: <script src="/plugin-ui/_sdk/bridge.js"></script>.
func (a *Admin) handlePluginSDK(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(a.adminFS, "js/pluginsdk.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

type bridgeRequest struct {
	Op     string          `json:"op"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Query  map[string]any  `json:"query"`
	Body   string          `json:"body"`
	Fn     string          `json:"fn"`
	Args   json.RawMessage `json:"args"`
}

type bridgeRouteReply struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
	Encoding    string `json:"encoding,omitempty"`
}

func (a *Admin) handlePluginBridge(w http.ResponseWriter, r *http.Request) {
	pk := a.deps.Growth.Packages
	cu := userFrom(r.Context())
	p, ok := a.uiPlugin(r.PathValue("name"), cu.Role)
	if pk == nil || !ok {
		a.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !a.bridgeLimit.allow(cu.ID + "/" + p.Name) {
		w.Header().Set("Retry-After", "1")
		a.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBridgeBody))
	var req bridgeRequest
	if err != nil || json.Unmarshal(body, &req) != nil {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	switch req.Op {
	case "route":
		if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, req.Method) || !strings.HasPrefix(req.Path, "/") ||
			len(req.Path) > 2048 || strings.ContainsAny(req.Path, "?#\\") {
			a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad route"})
			return
		}
		q := url.Values{}
		for k, v := range req.Query {
			q.Set(k, toString(v))
		}
		res := pk.CallRoute(r.Context(), p.Name, req.Method, strings.TrimPrefix(req.Path, "/"), q, []byte(req.Body), *cu)
		rep := bridgeRouteReply{Status: res.Status, ContentType: res.ContentType}
		if utf8.Valid(res.Body) {
			rep.Body = string(res.Body)
		} else {
			rep.Body, rep.Encoding = base64.StdEncoding.EncodeToString(res.Body), "base64"
		}
		a.writeJSON(w, http.StatusOK, rep)
	case "host":
		if cu.Role != "owner" {
			a.writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": map[string]string{"code": "permission_denied", "message": "host functions are available to the owner only; use the plugin's own routes"}})
			return
		}
		if !slices.Contains(p.UI.Functions, req.Fn) {
			a.writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": map[string]string{"code": "permission_denied", "message": req.Fn + " is not available to this plugin's page"}})
			return
		}
		out := pk.CallHost(r.Context(), p.Name, req.Fn, req.Args)
		h := w.Header()
		h.Set("Content-Type", "application/json; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		_, _ = w.Write(out)
	default:
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown op"})
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// bridgeLimiter is a token bucket per user and plugin: a looping page or a
// scripted editor cannot occupy the plugin's wasm pool.
type bridgeLimiter struct {
	mu sync.Mutex
	m  map[string]*bridgeBucket
}

type bridgeBucket struct {
	l    *rate.Limiter
	seen time.Time
}

const (
	bridgeRate  = 30 // calls per second
	bridgeBurst = 60
)

func (b *bridgeLimiter) allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]*bridgeBucket{}
	}
	now := time.Now()
	if len(b.m) > 1024 {
		for k, v := range b.m {
			if now.Sub(v.seen) > 5*time.Minute {
				delete(b.m, k)
			}
		}
	}
	e := b.m[key]
	if e == nil {
		e = &bridgeBucket{l: rate.NewLimiter(bridgeRate, bridgeBurst)}
		b.m[key] = e
	}
	e.seen = now
	return e.l.Allow()
}
