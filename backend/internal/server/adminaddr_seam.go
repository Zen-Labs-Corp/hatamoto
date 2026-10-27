package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/adminpath"
	"hatamoto.dev/core/internal/httpx"
)

// defaultAdminPrefix is the legacy admin address, used when no address was
// resolved at boot.
const defaultAdminPrefix = "/admin"

// adminPathEnv is the environment variable that pins the admin address.
const adminPathEnv = "HATAMOTO_ADMIN_PATH"

// adminBundle is one built admin: the address it answers at, the Admin and
// the handler that serves it. A bundle is complete before it is published, so
// a request sees the old one or the new one, never a half-built one.
type adminBundle struct {
	prefix string
	a      *admin.Admin
	h      http.Handler
}

// adminPathValue is the address the admin is served at: the live bundle's, else
// the value app boot resolved into the config, else /admin.
func (s *Server) adminPathValue() string {
	if b := s.adminBundle.Load(); b != nil {
		return b.prefix
	}
	if s.cfg != nil && s.cfg.Admin.Resolved != "" {
		return s.cfg.Admin.Resolved
	}
	return defaultAdminPrefix
}

// redactAdminPath hides the live admin address in a logged request path:
// <admin> replaces the prefix and the rest is kept. The cleaned form is tested
// too, because the mux redirects //pre/x or /a/../pre/x to the admin. As a
// backstop every other "/"-bounded copy of the prefix segment is replaced.
// Paths that never touch the prefix (including a scanner's wrong guess) are
// logged unchanged.
func (s *Server) redactAdminPath(p string) string {
	pre := s.adminPathValue()
	for _, cand := range []string{muxCleanPath(p), p} {
		if underPrefix(cand, pre) {
			return "<admin>" + hideSegment(strings.TrimPrefix(cand, pre), pre)
		}
	}
	return hideSegment(p, pre)
}

// hideSegment replaces each occurrence of pre (which starts with "/") in p
// that ends at a "/" or the end of p with "/<admin>".
func hideSegment(p, pre string) string {
	var b strings.Builder
	for {
		i := strings.Index(p, pre)
		if i < 0 {
			break
		}
		end := i + len(pre)
		if end == len(p) || p[end] == '/' {
			b.WriteString(p[:i])
			b.WriteString("/<admin>")
		} else {
			b.WriteString(p[:end])
		}
		p = p[end:]
	}
	return b.String() + p
}

// jsonNotFoundOrNotAllowed is the body of a mux-generated 404 or 405.
func jsonNotFoundOrNotAllowed(w http.ResponseWriter, status int) {
	msg := "not found"
	if status == http.StatusMethodNotAllowed {
		msg = "method not allowed"
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

// buildAdminBundle makes an Admin for prefix from d and mounts it on mux
// through g (a group over mux, or over the route recorder). Nothing is
// published here.
func (s *Server) buildAdminBundle(d admin.Deps, prefix string, mux *http.ServeMux, g *httpx.Group) (*adminBundle, error) {
	d.AdminPath = prefix
	build := s.newAdmin
	if build == nil {
		build = admin.New
	}
	a, err := build(d)
	if err != nil {
		return nil, err
	}
	a.Mount(g)
	return &adminBundle{prefix: prefix, a: a, h: httpx.WithErrorBodies(mux, jsonNotFoundOrNotAllowed)}, nil
}

// dispatcher serves the admin's address from the live bundle and everything
// else from the main mux.
func (s *Server) dispatcher() http.Handler {
	public := httpx.WithErrorBodies(s.mux, jsonNotFoundOrNotAllowed)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b := s.adminBundle.Load(); b != nil && underPrefix(r.URL.Path, b.prefix) {
			b.h.ServeHTTP(w, r)
			return
		}
		public.ServeHTTP(w, r)
	})
}

// routeSink is the registrar behind s.routes. While the first admin mounts, its
// patterns go to the admin's own mux, so the route table still lists them
// and a later address change can mount a new admin elsewhere. Two patterns
// do not depend on the address (/auth/, /plugin-ui/ and /-/edit); the main mux
// gets one fixed forwarder for each.
type routeSink struct {
	mux     *http.ServeMux
	admin   *http.ServeMux
	forward http.Handler
}

func (k *routeSink) Handle(pattern string, h http.Handler) {
	if k.admin == nil {
		k.mux.Handle(pattern, h)
		return
	}
	k.admin.Handle(pattern, h)
	_, p, _ := strings.Cut(pattern, " ")
	if strings.HasPrefix(p, "/auth/") || strings.HasPrefix(p, "/plugin-ui/") || p == "/-/edit" {
		k.mux.Handle(pattern, k.forward)
	}
}

// forwardToAdmin hands an address-independent admin request to the live admin.
func (s *Server) forwardToAdmin() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b := s.adminBundle.Load(); b != nil {
			b.h.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

// addressStore is the persisted admin address.
type addressStore interface {
	Get(ctx context.Context) (adminpath.Stored, error)
	Set(ctx context.Context, path, by string) error
	SetAck(ctx context.Context, path, by string) error
	Ack(ctx context.Context, path, by string) error
}

// serverAddress is the admin's address service (admin.AddressService).
type serverAddress struct{ s *Server }

var _ admin.AddressService = (*serverAddress)(nil)

// addressLock says why the address cannot be changed from the admin.
func (s *Server) addressLock() admin.AddressLock {
	switch {
	case s.cfg != nil && s.cfg.Demo.Enabled:
		return admin.AddressLockedDemo
	case s.cfg != nil && s.cfg.Admin.Path != "":
		return admin.AddressLockedEnv
	}
	return admin.AddressUnlocked
}

// refreshAddressState recomputes the cached state from the live address and
// the stored record. It runs at boot and after every write.
func (s *Server) refreshAddressState(ctx context.Context) {
	st := admin.AddressState{Path: s.adminPathValue(), Lock: s.addressLock()}
	if st.Lock == admin.AddressLockedEnv {
		st.Pinned = adminPathEnv
	}
	res := adminpath.Assess(ctx, st.Path, nil)
	st.Level, st.Reasons = res.Level, res.Reasons
	if s.addrStore != nil {
		if stored, err := s.addrStore.Get(ctx); err == nil {
			if stored.SetBy != nil {
				st.SetBy = *stored.SetBy
			}
			st.SetAt = stored.SetAt
			if stored.AckBy != nil {
				st.AckBy = *stored.AckBy
			}
			st.AckAt = stored.AckAt
		}
	}
	s.addrState.Store(&st)
}

func (a *serverAddress) State() admin.AddressState {
	if st := a.s.addrState.Load(); st != nil {
		st := *st
		st.Lock = a.s.addressLock()
		st.Pinned = ""
		if st.Lock == admin.AddressLockedEnv {
			st.Pinned = adminPathEnv
		}
		return st
	}
	return admin.AddressState{Path: a.s.adminPathValue(), Lock: a.s.addressLock()}
}

func (a *serverAddress) Assess(ctx context.Context, path string) adminpath.Result {
	return adminpath.Assess(ctx, path, a.s.newSiteReserved())
}

// Generate returns a strong random candidate, or "" when the system cannot
// draw one (the caller then keeps the current address).
func (a *serverAddress) Generate() string {
	p, err := adminpath.Generate()
	if err != nil {
		a.s.logger.Error("generate admin address", "error", err)
		return ""
	}
	return p
}

func (a *serverAddress) refused(why string) error {
	return fmt.Errorf("%w: %s", admin.ErrAddressRefused, why)
}

// Apply stores req.Path and serves the admin there from now on. The new admin
// is built before anything is stored, and the store is written before it is
// published, so a failure at either step leaves the old address serving.
func (a *serverAddress) Apply(ctx context.Context, req admin.ApplyRequest) (admin.ApplyResult, error) {
	s := a.s
	if s.addressLock() != admin.AddressUnlocked {
		return admin.ApplyResult{}, a.refused("the address cannot be changed here")
	}
	res := a.Assess(ctx, req.Path)
	switch res.Level {
	case adminpath.LevelInvalid, adminpath.LevelReserved:
		why := res.Level.String()
		if len(res.Reasons) > 0 {
			why = res.Reasons[0].Text
		}
		return admin.ApplyResult{}, a.refused(why)
	case adminpath.LevelWeak:
		if !req.Ack {
			return admin.ApplyResult{}, a.refused("a weak address needs to be confirmed")
		}
	}
	path := res.Path

	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	old := s.adminBundle.Load()
	var nb *adminBundle
	if old == nil || old.prefix != path {
		mux := http.NewServeMux()
		var err error
		nb, err = s.buildAdminBundle(s.adminDeps, path, mux, httpx.NewGroup(mux, ""))
		if err != nil {
			return admin.ApplyResult{}, fmt.Errorf("build admin at the new address: %w", err)
		}
	}
	var err error
	if res.Level == adminpath.LevelWeak {
		err = s.addrStore.SetAck(ctx, path, req.By)
	} else {
		err = s.addrStore.Set(ctx, path, req.By)
	}
	if err != nil {
		return admin.ApplyResult{}, err
	}
	if nb != nil {
		s.adminBundle.Store(nb)
		s.rememberOldPrefix(old, path)
	}
	s.refreshAddressState(ctx)
	return admin.ApplyResult{Path: path}, nil
}

// Keep records that an owner knowingly accepted the current address.
func (a *serverAddress) Keep(ctx context.Context, by string) error {
	s := a.s
	if s.addressLock() != admin.AddressUnlocked {
		return a.refused("the address cannot be changed here")
	}
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	// Accepting is not choosing: keep who chose the address and when. Only a
	// row with no stored address (an upgrade) has no choice to keep.
	path := s.adminPathValue()
	err := s.addrStore.Ack(ctx, path, by)
	if errors.Is(err, adminpath.ErrNoSiteConfig) {
		// Only a row with no stored address may take the live one. Any other
		// stored path is newer than this server's: do not write over it.
		if stored, gerr := s.addrStore.Get(ctx); gerr == nil && stored.Path == nil {
			err = s.addrStore.SetAck(ctx, path, by)
		} else {
			err = a.refused("the admin address changed on the server. Reload the page")
		}
	}
	if err != nil {
		return err
	}
	s.refreshAddressState(ctx)
	return nil
}

// rememberOldPrefix adds the address that just stopped serving to the list the
// intercept uses, and drops the new live address from it. Callers hold adminMu.
func (s *Server) rememberOldPrefix(old *adminBundle, live string) {
	var next []string
	if cur := s.oldPrefixes.Load(); cur != nil {
		for _, p := range *cur {
			if p != live {
				next = append(next, p)
			}
		}
	}
	if old != nil && old.prefix != live {
		next = append(next, old.prefix)
	}
	s.oldPrefixes.Store(&next)
}

// oldAdminPrefixes are the addresses the admin used to have, newest last.
func (s *Server) oldAdminPrefixes() []string {
	if p := s.oldPrefixes.Load(); p != nil {
		return *p
	}
	return nil
}
