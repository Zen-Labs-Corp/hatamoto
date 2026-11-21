package admin

import (
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/adminpagestate"
	"hatamoto.dev/core/internal/httpx"
)

// mountModulePages registers the routes of the module pages on the owner
// group, so every one inherits the session, role, CSRF and header guards. A
// route marked Sudo also needs a recent password confirmation. With no module
// pages nothing is added.
func (a *Admin) mountModulePages(owner *httpx.Group) {
	if len(a.deps.ModulePages) == 0 {
		return
	}
	sudo := owner.Group("", a.RequireSudo)
	for _, p := range a.deps.ModulePages {
		for _, rt := range p.Routes {
			g := owner
			if rt.Sudo {
				g = sudo
			}
			g.HandleFunc(rt.Method+" /settings/modules/"+p.ID+rt.Path, a.modulePage(p, rt))
		}
	}
}

// modulePageSets adds the module templates to pages under "module/<id>/<name>".
// A module whose template tree cannot be listed adds nothing, so its routes
// fail closed on an unknown page instead of breaking the admin build.
func (a *Admin) modulePageSets(pages map[string]*pageSet, srcs *pageSources, fm template.FuncMap, lru *execLRU) {
	for _, p := range a.deps.ModulePages {
		err := fs.WalkDir(p.Templates, "pages", func(file string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(file, ".html") {
				return nil
			}
			name := strings.TrimSuffix(strings.TrimPrefix(file, "pages/"), ".html")
			pages["module/"+p.ID+"/"+name] = &pageSet{fsys: p.Templates, file: file, base: srcs.base, fm: fm, lru: lru}
			return nil
		})
		if err != nil {
			a.logger().Error("admin: module templates", "module", p.ID, "err", err)
		}
	}
}

// modulePage runs a module handler. An error or a panic that happens before the
// handler answered becomes the core error page, never a plain or half page. So
// does a handler that returns nil without writing a response.
func (a *Admin) modulePage(p adminpagestate.Page, rt adminpagestate.Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := &moduleCtx{a: a, w: w, r: r, p: p}
		defer func() {
			if rec := recover(); rec != nil {
				a.logger().Error("admin: module page panic", "module", p.ID, "panic", rec)
				c.fail()
			}
		}()
		if err := rt.Handle(c); err != nil {
			a.logger().Error("admin: module page", "module", p.ID, "err", err)
			c.fail()
			return
		}
		if !c.done {
			a.logger().Error("admin: module page wrote no response", "module", p.ID)
			c.fail()
		}
	}
}

// moduleCtx is the adminpagestate.Context core hands to a module handler.
type moduleCtx struct {
	a    *Admin
	w    http.ResponseWriter
	r    *http.Request
	p    adminpagestate.Page
	done bool // a response was written
}

func (c *moduleCtx) fail() {
	if c.done {
		return
	}
	c.Error(http.StatusInternalServerError, "Something went wrong", "This page could not be shown.")
}

func (c *moduleCtx) Request() *http.Request { return c.r }

func (c *moduleCtx) User() adminpagestate.User {
	u := userFrom(c.r.Context())
	if u == nil {
		return adminpagestate.User{}
	}
	return adminpagestate.User{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role}
}

func (c *moduleCtx) CSRF() string { return csrfToken(c.r.Context()) }

func (c *moduleCtx) URL(parts ...string) string {
	all := []string{"settings", "modules", c.p.ID}
	for _, p := range parts {
		if p != "" {
			all = append(all, p)
		}
	}
	return c.a.url(all...)
}

func (c *moduleCtx) Render(status int, page, title string, data any) error {
	v := c.a.NewView(c.w, c.r, title)
	v.Data = data
	if err := c.a.render(c.w, c.r, status, "module/"+c.p.ID+"/"+page, v); err != nil {
		c.r = withBannersDone(c.r) // NewView above already asked the providers
		c.fail()
		return err
	}
	c.done = true
	return nil
}

func (c *moduleCtx) Redirect(rel string) {
	c.done = true
	http.Redirect(c.w, c.r, c.URL(rel), http.StatusSeeOther)
}

func (c *moduleCtx) Flash(kind, msg string) { c.a.SetFlash(c.w, c.r, kind, msg) }

func (c *moduleCtx) Error(status int, heading, msg string) {
	c.done = true
	c.a.Error(c.w, c.r, status, heading, msg)
}

func (c *moduleCtx) CheckDanger(dc adminpagestate.DangerConfirm) (adminpagestate.DangerConfirm, bool) {
	return CheckDangerPhrase(c.r, dc)
}
