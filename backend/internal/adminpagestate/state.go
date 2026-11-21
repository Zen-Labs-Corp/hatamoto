// Package adminpagestate holds the module admin pages and banners that the
// public hatamoto.dev/core/adminpages package exposes. Only core calls
// Freeze, Pages, Banners and Reset; wrapper modules use the public package.
package adminpagestate

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/admin/ui"
)

// User is the signed-in admin user a module handler sees.
type User struct{ ID, Name, Email, Role string }

// DangerConfirm is core's danger-confirm contract.
type DangerConfirm = ui.DangerConfirm

// Context is what core hands to a module route handler. Only core implements it.
type Context interface {
	Request() *http.Request
	User() User
	CSRF() string
	URL(parts ...string) string
	Render(status int, page, title string, data any) error
	Redirect(rel string)
	Flash(kind, msg string)
	Error(status int, heading, msg string)
	CheckDanger(dc DangerConfirm) (DangerConfirm, bool)
}

// HandlerFunc handles one module route. A returned error renders core's error page.
type HandlerFunc func(c Context) error

// Route is one HTTP route of a module page, relative to the page URL.
type Route struct {
	Method, Path string
	Sudo         bool
	Handle       HandlerFunc
}

// Page is one owner-only Settings page of a module.
type Page struct {
	ID, Label, Desc, Icon string
	Templates             fs.FS
	Routes                []Route
}

// Banner is one owner banner a module shows in the admin shell.
type Banner struct{ Kind, Message, Href, Label string }

// BannerFunc returns the banners to show now.
type BannerFunc func(ctx context.Context) ([]Banner, error)

// NamedBanner is a registered banner provider.
type NamedBanner struct {
	Name string
	Fn   BannerFunc
}

var (
	mu      sync.Mutex
	frozen  bool
	pages   []Page
	banners []NamedBanner
	validID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

const frozenHint = " called after app.Main started; register from an init function"

// Register adds a page. It panics after Freeze, on a bad or duplicate ID, an
// empty label, nil templates, a missing GET "" route, a nil handler, a method
// other than GET or POST, or a path that is not a valid route suffix.
func Register(p Page) {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		panic("adminpages: Register" + frozenHint)
	}
	if err := validate(p); err != nil {
		panic("adminpages: " + err.Error())
	}
	pages = append(pages, clonePage(p))
}

func validate(p Page) error {
	switch {
	case !validID.MatchString(p.ID):
		return fmt.Errorf("invalid page id %q", p.ID)
	case slices.ContainsFunc(pages, func(q Page) bool { return q.ID == p.ID }):
		return fmt.Errorf("page %q registered twice", p.ID)
	case strings.TrimSpace(p.Label) == "":
		return fmt.Errorf("page %q has no label", p.ID)
	case p.Templates == nil:
		return fmt.Errorf("page %q has no templates", p.ID)
	}
	if err := probeTemplates(p.Templates); err != nil {
		return fmt.Errorf("page %q: templates not usable: %v", p.ID, err)
	}
	hasIndex := false
	trial := http.NewServeMux() // one mux, so routes of the page that conflict are caught here
	for _, rt := range p.Routes {
		if rt.Method != http.MethodGet && rt.Method != http.MethodPost {
			return fmt.Errorf("page %q: method %q is not GET or POST", p.ID, rt.Method)
		}
		if rt.Handle == nil {
			return fmt.Errorf("page %q: nil handler for %s %q", p.ID, rt.Method, rt.Path)
		}
		if rt.Path != "" && !strings.HasPrefix(rt.Path, "/") || strings.Contains(rt.Path, "..") {
			return fmt.Errorf("page %q: invalid route path %q", p.ID, rt.Path)
		}
		if err := trialPattern(trial, rt.Method+" /settings/modules/"+p.ID+rt.Path); err != nil {
			return fmt.Errorf("page %q: bad route pattern %q: %v", p.ID, rt.Path, err)
		}
		hasIndex = hasIndex || rt.Method == http.MethodGet && rt.Path == ""
	}
	if !hasIndex {
		return fmt.Errorf("page %q has no GET route with an empty path", p.ID)
	}
	return nil
}

// probeTemplates catches a typed-nil FS (for example a nil *embed.FS), which
// passes a nil check but panics when core lists the templates at boot. A
// missing pages directory is fine: the page then fails closed at run time.
func probeTemplates(fsys fs.FS) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	_, _ = fs.Stat(fsys, "pages")
	return nil
}

func trialPattern(mux *http.ServeMux, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	return nil
}

func clonePage(p Page) Page {
	p.Routes = slices.Clone(p.Routes)
	return p
}

// RegisterBanner adds a banner provider. It panics after Freeze, on a bad or
// duplicate name, or with a nil func.
func RegisterBanner(name string, fn BannerFunc) {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case frozen:
		panic("adminpages: RegisterBanner" + frozenHint)
	case !validID.MatchString(name):
		panic(fmt.Sprintf("adminpages: invalid banner name %q", name))
	case slices.ContainsFunc(banners, func(b NamedBanner) bool { return b.Name == name }):
		panic(fmt.Sprintf("adminpages: banner %q registered twice", name))
	case fn == nil:
		panic(fmt.Sprintf("adminpages: banner %q has a nil func", name))
	}
	banners = append(banners, NamedBanner{Name: name, Fn: fn})
}

// Pages returns the registered pages in registration order.
func Pages() []Page {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Page, len(pages))
	for i, p := range pages {
		out[i] = clonePage(p)
	}
	return out
}

// Banners returns the registered banner providers in registration order.
func Banners() []NamedBanner {
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(banners)
}

// Freeze makes any later Register panic. Core calls it first thing in Main.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Reset clears all state; tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	frozen = false
	pages = nil
	banners = nil
}
