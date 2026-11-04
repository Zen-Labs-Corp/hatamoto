package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/siteaccess"
)

const accessBannerMarker = "data-hm-access-banner"

func TestAccessBanner(t *testing.T) {
	t.Parallel()

	for mode, want := range map[siteaccess.Mode]string{
		siteaccess.ModeComingSoon:  "Your site is in coming soon mode. Visitors cannot see it. Visitors see a coming soon page.",
		siteaccess.ModeMaintenance: "Your site is in maintenance mode. Visitors cannot see it. Visitors see a maintenance page.",
		siteaccess.ModePassword:    "Your site is in password mode. Visitors cannot see it. Visitors need a password.",
	} {
		p := newSiteAccessRig(t, &fakeSiteAccess{settings: siteaccess.Settings{Mode: mode, RetryAfter: 3600}})
		for _, path := range []string{"/admin/settings/general", "/admin/settings/site-access"} {
			body := p.do("GET", path, "owner-token", nil, nil).Body.String()
			if !strings.Contains(body, accessBannerMarker+`="`+string(mode)+`"`) || !strings.Contains(body, want) || !strings.Contains(body, `href="/admin/settings/site-access"`) {
				t.Errorf("%s owner %s: banner missing", mode, path)
			}
		}
		// settings/general is owner only; redirects is open to editors.
		body := p.do("GET", "/admin/settings/redirects", "editor-token", nil, nil).Body.String()
		if !strings.Contains(body, want) {
			t.Errorf("%s: editor sees no banner", mode)
		}
		if strings.Contains(body, `href="/admin/settings/site-access"`) {
			t.Errorf("%s: editor gets a link to a page they cannot open", mode)
		}
	}
}

func TestAccessBannerAbsent(t *testing.T) {
	t.Parallel()

	p := newSiteAccessRig(t, &fakeSiteAccess{settings: siteaccess.Settings{Mode: siteaccess.ModeOpen, RetryAfter: 3600}})
	if body := p.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, accessBannerMarker) {
		t.Error("banner shown while the site is open")
	}
	p = newPerfRig(t, nil)
	if body := p.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String(); strings.Contains(body, accessBannerMarker) {
		t.Error("banner shown without a service")
	}
	// A page that changes nothing but answers a POST shows no banner.
	p = newSiteAccessRig(t, &fakeSiteAccess{settings: siteaccess.Settings{Mode: siteaccess.ModeMaintenance, RetryAfter: 3600}})
	if body := p.do("POST", "/admin/settings/site-access", "owner-token", map[string][]string{"mode": {"nope"}}, nil).Body.String(); strings.Contains(body, accessBannerMarker) {
		t.Error("banner shown on a POST response")
	}
}

func TestAccessBannerGolden(t *testing.T) {
	t.Parallel()

	p := newSiteAccessRig(t, &fakeSiteAccess{settings: siteaccess.Settings{Mode: siteaccess.ModeMaintenance, RetryAfter: 3600}})
	body := p.do("GET", "/admin/settings/general", "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "shell_site_access.golden", normalizePage(body))
}
