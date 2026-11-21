package admin

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ModuleBanner is a notice a compiled-in module shows to owners above every
// admin page. Href is absolute (the admin prefix is already joined in).
type ModuleBanner struct {
	Module, Kind, Message, Href, Label string
}

type bannersDoneKey struct{}

// withBannersDone marks r so the providers are not asked again, for example by
// the error page that follows a view whose providers already ran.
func withBannersDone(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), bannersDoneKey{}, true))
}

// moduleBannerTimeout bounds every provider together, so a slow module cannot
// stall the page.
const moduleBannerTimeout = 300 * time.Millisecond

// moduleBanners asks every provider for banners. Only owners see them. A
// provider that fails, panics, times out or returns an invalid banner shows
// nothing; the page renders regardless. Fragment requests render no shell, so
// they skip the providers.
func (a *Admin) moduleBanners(r *http.Request, u *CurrentUser) []ModuleBanner {
	if u == nil || u.Role != "owner" || len(a.deps.ModuleBanners) == 0 || isFragment(r) || r.Context().Value(bannersDoneKey{}) != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), moduleBannerTimeout)
	defer cancel()
	got := make([][]ModuleBanner, len(a.deps.ModuleBanners))
	fns := make([]func(context.Context) error, len(a.deps.ModuleBanners))
	for i, nb := range a.deps.ModuleBanners {
		fns[i] = func(ctx context.Context) error {
			bs, err := nb.Fn(ctx)
			if err != nil {
				return err
			}
			for _, b := range bs {
				if mb, ok := a.moduleBanner(nb.Name, b.Kind, b.Message, b.Href, b.Label); ok {
					got[i] = append(got[i], mb)
				}
			}
			return nil
		}
	}
	var out []ModuleBanner
	for i, err := range a.runDash(ctx, fns) {
		if err != nil {
			a.logger().Error("admin: module banner", "module", a.deps.ModuleBanners[i].Name, "err", err)
			continue
		}
		out = append(out, got[i]...)
	}
	return out
}

// moduleBanner validates one banner: a known kind and a message are required;
// an Href that is not an admin-relative path is dropped, not the banner.
func (a *Admin) moduleBanner(module, kind, message, href, label string) (ModuleBanner, bool) {
	switch kind {
	case "danger", "warning", "info":
	default:
		return ModuleBanner{}, false
	}
	if message == "" {
		return ModuleBanner{}, false
	}
	b := ModuleBanner{Module: module, Kind: kind, Message: message, Label: label}
	if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		b.Href = a.adminPath + href
	}
	if b.Label == "" {
		b.Label = "Open"
	}
	return b, true
}
