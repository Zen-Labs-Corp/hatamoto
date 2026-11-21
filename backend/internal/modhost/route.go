package modhost

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/adminpath"
)

const (
	// DefaultRouteMaxBody is the request body cap when a Route sets none.
	DefaultRouteMaxBody = 1 << 20
	// MaxRouteMaxBody is the largest body cap a Route may ask for.
	MaxRouteMaxBody = 32 << 20
)

// Route mounts Handler for every request under Prefix.
type Route struct {
	Module  string
	Prefix  string // "/name/" or "/name/sub/": lower case, trailing slash
	Handler http.Handler
	MaxBody int64 // 0 means DefaultRouteMaxBody
}

var routes []Route

var routePrefix = regexp.MustCompile(`^/[a-z0-9][a-z0-9_-]*(/[a-z0-9][a-z0-9._-]*)*/$`)

// CoreAuthPaths are the second segments core serves under /auth/: the legacy
// sign-in redirect for /auth/login and /auth/register. Everything else under
// /auth/<name>/ may be mounted by a module. A server test checks this list
// against the live route table.
var CoreAuthPaths = []string{"login", "register"}

func init() { onReset(func() { routes = nil }) }

// RouteRefusal reports why prefix may not be mounted, or "" when it may.
func RouteRefusal(prefix string) string {
	if !routePrefix.MatchString(prefix) {
		return "must look like /name/ (lower case, trailing slash)"
	}
	segs := strings.Split(strings.Trim(prefix, "/"), "/")
	seg := segs[0]
	switch {
	case seg == "admin" || seg == "api":
		return "core serves /" + seg
	case seg == "auth":
		if len(segs) < 2 {
			return "core serves /auth/"
		}
		if slices.Contains(CoreAuthPaths, segs[1]) {
			return "core serves /auth/" + segs[1]
		}
		return ""
	}
	return adminpath.RedirectPathRefusal("/" + seg)
}

// RegisterRoute adds a route mount. It panics after Freeze, on an invalid
// module name, nil handler, refused prefix, bad body cap or a prefix that
// overlaps one already registered.
func RegisterRoute(r Route) {
	register("route: Register", func() {
		switch {
		case !ValidName(r.Module):
			panic(fmt.Sprintf("route: invalid module name %q", r.Module))
		case isNil(r.Handler):
			panic(fmt.Sprintf("route: module %q has a nil handler", r.Module))
		}
		if why := RouteRefusal(r.Prefix); why != "" {
			panic(fmt.Sprintf("route: module %q prefix %q refused: %s", r.Module, r.Prefix, why))
		}
		if r.MaxBody < 0 || r.MaxBody > MaxRouteMaxBody {
			panic(fmt.Sprintf("route: module %q max body %d out of range", r.Module, r.MaxBody))
		}
		if r.MaxBody == 0 {
			r.MaxBody = DefaultRouteMaxBody
		}
		for _, have := range routes {
			if strings.HasPrefix(have.Prefix, r.Prefix) || strings.HasPrefix(r.Prefix, have.Prefix) {
				panic(fmt.Sprintf("route: module %q prefix %q overlaps %q of module %q", r.Module, r.Prefix, have.Prefix, have.Module))
			}
		}
		routes = append(routes, r)
	})
}

// Routes returns the registered routes in registration order.
func Routes() []Route {
	mu.Lock()
	defer mu.Unlock()
	return append([]Route(nil), routes...)
}
