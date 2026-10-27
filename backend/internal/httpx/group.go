// Package httpx is the thin layer over net/http.ServeMux that gives the
// server prefixed, middleware-carrying route groups and a record of every
// registered pattern (for the route-table test and `hatamoto routes`).
//
// Routing rule: the mux is populated once at boot and never changes; anything
// dynamic (plugin routes) is resolved inside fixed catch-all patterns.
package httpx

import (
	"fmt"
	"net/http"
	"reflect"
	"runtime"
	"strings"
)

// Middleware wraps a handler.
type Middleware = func(http.Handler) http.Handler

// Router is the registration surface handed to route mounters.
type Router interface {
	Handle(pattern string, h http.Handler)
	HandleFunc(pattern string, h http.HandlerFunc)
}

// registrar is where a Group's patterns end up: a ServeMux, or a recording
// sink when a plugin's routes are staged (see Dispatch.Add).
type registrar interface {
	Handle(pattern string, h http.Handler)
}

// Route is one registered pattern and the name of its handler.
type Route struct {
	Pattern string // "METHOD /full/path" as given to the mux
	Handler string
}

// Group registers patterns under a path prefix, wrapping every handler in the
// group's middleware.
type Group struct {
	mux    registrar
	prefix string
	mw     []Middleware
	routes *[]Route
}

var _ Router = (*Group)(nil)

// NewGroup returns the root group of mux; all groups derived from it share one
// route record.
func NewGroup(mux registrar, prefix string, mw ...Middleware) *Group {
	return &Group{mux: mux, prefix: prefix, mw: mw, routes: new([]Route)}
}

// Group returns a nested group: prefix is appended and mw run inside the
// parent's middleware.
func (g *Group) Group(prefix string, mw ...Middleware) *Group {
	return &Group{
		mux:    g.mux,
		prefix: g.prefix + prefix,
		mw:     append(append([]Middleware(nil), g.mw...), mw...),
		routes: g.routes,
	}
}

// HandleFunc is Handle for a handler function.
func (g *Group) HandleFunc(pattern string, h http.HandlerFunc) { g.Handle(pattern, h) }

// Handle registers pattern ("[METHOD ]/path", path relative to the group). A
// path of "/" matches the group's prefix with or without a trailing slash,
// and nothing below it. A method is required (a method-less pattern would
// conflict with the catch-alls). Invalid or conflicting patterns panic at
// registration.
func (g *Group) Handle(pattern string, h http.Handler) {
	method, path, hasMethod := strings.Cut(pattern, " ")
	if !hasMethod || strings.ContainsAny(method, "/{") {
		panic(fmt.Sprintf("httpx: pattern %q has no method: use \"METHOD /path\"", pattern))
	}
	if !strings.HasPrefix(path, "/") {
		panic(fmt.Sprintf("httpx: pattern %q: the path must start with \"/\" (host patterns are not supported)", pattern))
	}
	method += " "
	name := handlerName(h)
	wrapped := Chain(g.mw...)(h)

	full := g.prefix + path
	if path == "/" {
		full = g.prefix + "/{$}"
		if g.prefix != "" {
			g.register(method+g.prefix, name, wrapped)
		}
	}
	g.register(method+full, name, wrapped)
}

func (g *Group) register(pattern, name string, h http.Handler) {
	defer func() {
		if r := recover(); r != nil {
			panic(fmt.Sprintf("httpx: cannot register route %q: %v", pattern, r))
		}
	}()
	g.mux.Handle(pattern, h)
	*g.routes = append(*g.routes, Route{Pattern: pattern, Handler: name})
}

// Routes returns every pattern registered through the group tree so far.
func (g *Group) Routes() []Route { return append([]Route(nil), *g.routes...) }

// Chain composes middleware; the first runs outermost.
func Chain(mw ...Middleware) Middleware {
	return func(h http.Handler) http.Handler {
		for i := len(mw) - 1; i >= 0; i-- {
			h = mw[i](h)
		}
		return h
	}
}

func handlerName(h http.Handler) string {
	if f, ok := h.(http.HandlerFunc); ok {
		if fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer()); fn != nil {
			return fn.Name()
		}
	}
	return fmt.Sprintf("%T", h)
}
