package pluginhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"hatamoto.dev/core/internal/wasmhost"
	"hatamoto.dev/sdk"
)

// Plugin routes are declared in the manifest and served by two fixed
// catch-alls in the server (/api/v1/p/{plugin}/{rest...} behind auth and CSRF,
// /p/{plugin}/{rest...} public and rate limited). Each plugin has its own small
// router; the routers live in an immutable map behind an atomic pointer, so
// adding, replacing or removing a plugin's routes is one pointer swap and the
// request path takes no lock. Core routes are never shadowed: the plugin's
// path is only ever looked up below its fixed prefix.

// Limits of a plugin HTTP exchange.
const (
	maxRouteBodyBytes = 1 << 20
	routeExport       = "http_handle"
)

// Headers forwarded to and from the plugin.
var (
	requestHeaders  = []string{"Accept", "Accept-Language", "Content-Type", "User-Agent", "Referer", "Origin", "X-Request-Id"}
	responseHeaders = []string{"Content-Type", "Cache-Control", "Location", "Content-Disposition", "Etag"}
)

// pluginSandboxCSP is set on every plugin response: HTML a plugin returns runs
// without scripts and in an opaque origin, so it cannot act as the site or the
// admin.
const pluginSandboxCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src https: data:"

// Caller runs an export of a plugin (wasmhost.Host.Call).
type Caller func(ctx context.Context, plugin, export string, in []byte) ([]byte, error)

type pluginRouter struct {
	mux    *http.ServeMux
	routes map[string]sdk.Route // by "METHOD /path" pattern
}

// RouteRegistry holds the per-plugin routers of one mount (admin or public).
type RouteRegistry struct {
	call   Caller
	log    *slog.Logger
	public bool
	m      atomic.Pointer[map[string]*pluginRouter]
}

// NewRouteRegistry returns a registry serving either the admin or the public
// routes of the plugins.
func NewRouteRegistry(call Caller, public bool, log *slog.Logger) *RouteRegistry {
	if log == nil {
		log = slog.Default()
	}
	g := &RouteRegistry{call: call, log: log, public: public}
	empty := map[string]*pluginRouter{}
	g.m.Store(&empty)
	return g
}

// Set replaces plugin's router with one built from routes (only the routes of
// this registry's mount are used). No routes removes it. It is safe to call
// while requests are being served.
func (g *RouteRegistry) Set(plugin string, routes []sdk.Route) {
	var pr *pluginRouter
	for _, r := range routes {
		if r.Public != g.public {
			continue
		}
		if pr == nil {
			pr = &pluginRouter{mux: http.NewServeMux(), routes: map[string]sdk.Route{}}
		}
		pattern := r.Method + " " + r.Path
		pr.mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }) // matcher only; validated by the manifest
		pr.routes[pattern] = r
	}
	for {
		old := g.m.Load()
		next := make(map[string]*pluginRouter, len(*old)+1)
		for k, v := range *old {
			next[k] = v
		}
		if pr == nil {
			delete(next, plugin)
		} else {
			next[plugin] = pr
		}
		if g.m.CompareAndSwap(old, &next) {
			return
		}
	}
}

// Plugins lists the plugins that have routes here, sorted.
func (g *RouteRegistry) Plugins() []string {
	var out []string
	for k := range *g.m.Load() {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Serve handles the request for plugin; rest is the path below the mount
// (without the leading slash). user is nil on the public mount.
func (g *RouteRegistry) Serve(w http.ResponseWriter, r *http.Request, plugin, rest string, user *sdk.HTTPUser) {
	pr := (*g.m.Load())[plugin]
	if pr == nil {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	path := "/" + rest
	// The path that is authorised is the path the plugin receives, byte for
	// byte: anything the mux would clean or redirect (".." segments, "//",
	// a missing trailing slash) is a 404, never a different route.
	if cleanRoutePath(path) != path {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	probe := r.Clone(r.Context())
	probe.URL.Path, probe.URL.RawPath, probe.RequestURI = path, "", path
	pw := &probeWriter{h: http.Header{}}
	pr.mux.ServeHTTP(pw, probe)
	switch pw.status {
	case http.StatusNoContent: // a route handler ran
	case http.StatusMethodNotAllowed:
		if allow := pw.h.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	default: // not found, or the mux wants to redirect to another path
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	pattern := probe.Pattern
	route := pr.routes[pattern]
	if !g.public {
		roles := route.Roles
		if len(roles) == 0 {
			roles = []string{"owner"}
		}
		if user == nil || !slices.Contains(roles, user.Role) {
			writeJSONError(w, http.StatusForbidden, "forbidden")
			return
		}
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRouteBodyBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	req := sdk.HTTPRequest{Method: r.Method, Path: path, Pattern: route.Path, PathValues: pathValues(route.Path, probe),
		Query: r.URL.Query(), Headers: map[string]string{}, User: user}
	for _, h := range requestHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Headers[strings.ToLower(h)] = v
		}
	}
	if utf8.Valid(body) {
		req.Body = string(body)
	} else {
		req.BodyB64 = base64.StdEncoding.EncodeToString(body)
	}
	in, _ := json.Marshal(req)
	out, err := g.call(r.Context(), plugin, routeExport, in)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, wasmhost.ErrCircuitOpen) || errors.Is(err, wasmhost.ErrSaturated) || errors.Is(err, wasmhost.ErrNotLoaded) {
			status = http.StatusServiceUnavailable
		}
		g.log.Warn("plugin route failed", slog.String("plugin", plugin), slog.String("path", path), slog.Any("error", err))
		writeJSONError(w, status, "plugin error")
		return
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		g.log.Warn("plugin route: bad response", slog.String("plugin", plugin), slog.Any("error", err))
		writeJSONError(w, http.StatusBadGateway, "plugin error")
		return
	}
	g.writeRouteResponse(w, r, res)
}

func (g *RouteRegistry) writeRouteResponse(w http.ResponseWriter, r *http.Request, res sdk.HTTPResponse) {
	status := res.Status
	if status == 0 {
		status = http.StatusOK
	}
	if status < 200 || status > 599 {
		writeJSONError(w, http.StatusBadGateway, "plugin error")
		return
	}
	body := []byte(res.Body)
	if res.BodyB64 != "" {
		var err error
		if body, err = base64.StdEncoding.DecodeString(res.BodyB64); err != nil {
			writeJSONError(w, http.StatusBadGateway, "plugin error")
			return
		}
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	for _, name := range responseHeaders {
		for k, v := range res.Headers {
			if strings.EqualFold(k, name) && !hasControlByte(v) {
				h.Set(name, v)
			}
		}
	}
	if loc := h.Get("Location"); loc != "" && !safeLocation(loc) {
		// A redirect leaves for another origin only through the plugin's own
		// code paths (http.fetch); the site never does it on its behalf.
		g.log.Warn("plugin route: Location is not a same-site path", slog.String("location", loc))
		h.Del("Location")
		writeJSONError(w, http.StatusBadGateway, "plugin error")
		return
	}
	if !g.public {
		// Admin responses are per user: never shared-cacheable.
		if cc := strings.ToLower(h.Get("Cache-Control")); strings.Contains(cc, "public") || strings.Contains(cc, "s-maxage") {
			h.Set("Cache-Control", "no-store")
		} else if !strings.Contains(cc, "private") && !strings.Contains(cc, "no-store") {
			h.Set("Cache-Control", "private, "+h.Get("Cache-Control"))
		}
	}
	if h.Get("Content-Type") == "" && len(body) > 0 {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	h.Set("Content-Security-Policy", pluginSandboxCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// cleanRoutePath is p as the mux would clean it, keeping a trailing slash.
func cleanRoutePath(p string) string {
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c
}

// safeLocation allows only a same-site path: "/x", not "//host" or "/\host".
// Browsers strip tabs and newlines before resolving a URL, so any control or
// space byte is refused outright ("/\t/evil.example" would become "//evil.example").
func safeLocation(l string) bool {
	for i := 0; i < len(l); i++ {
		if l[i] <= 0x20 || l[i] == 0x7f {
			return false
		}
	}
	if !strings.HasPrefix(l, "/") || strings.HasPrefix(l, "//") || strings.HasPrefix(l, "/\\") {
		return false
	}
	u, err := url.Parse(l)
	return err == nil && u.Scheme == "" && u.Host == "" && u.Opaque == "" && strings.HasPrefix(u.Path, "/")
}

// probeWriter records what the matcher mux answered. A matched route's
// handler writes 204; anything else (404, 405, a 301/308 redirect) is the
// mux's own.
type probeWriter struct {
	h      http.Header
	status int
}

func (p *probeWriter) Header() http.Header         { return p.h }
func (p *probeWriter) Write(b []byte) (int, error) { return len(b), nil }
func (p *probeWriter) WriteHeader(code int) {
	if p.status == 0 {
		p.status = code
	}
}

var wildcardRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(\.\.\.)?\}`)

// pathValues returns the wildcard values of the matched route pattern.
func pathValues(routePath string, r *http.Request) map[string]string {
	var out map[string]string
	for _, m := range wildcardRe.FindAllStringSubmatch(routePath, -1) {
		if out == nil {
			out = map[string]string{}
		}
		out[m[1]] = r.PathValue(m[1])
	}
	return out
}

// hasControlByte reports a CR, LF, tab or other control byte, none of which
// belongs in a header value a plugin supplies.
func hasControlByte(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 && v[i] != '\t' || v[i] == 0x7f {
			return true
		}
	}
	return false
}
