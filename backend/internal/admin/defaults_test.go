package admin

import (
	"io/fs"
	"net/http"
	"net/url"
	"time"

	"hatamoto.dev/core/internal/admin/ui"
)

// The helpers below pin the default /admin prefix for tests. Production code
// must take the prefix from the Admin (a.adminPath, a.url) and never call
// them; keeping them in a _test.go file makes a stray production call a compile
// error, and TestUILintNoDefaultAdminPath fails a new DefaultAdminPath use.

// legacyTarget is legacyTargetIn the default-prefix table.
func legacyTarget(u *url.URL) (string, bool) {
	return legacyTargetIn(legacyRulesFor(DefaultAdminPath), u)
}

// legacyRedirect answers 301 (GET, HEAD) or 308 (any other method) on an old
// admin URL, before the session and CSRF checks so a sign-in "next" never
// carries an old path.
func legacyRedirect(next http.Handler) http.Handler {
	return legacyRedirectFor(legacyRulesFor(DefaultAdminPath), next)
}

// backupsPath is backupsRel under the default prefix, for tests.
const backupsPath = DefaultAdminPath + backupsRel

// DefaultNav registers the core sidebar: Overview, then Site (Entities, AI
// analytics) and Customize (Plugins, Theme, Settings). Theme and Settings carry a secondary sidebar of their
// own (see adminArea).
func DefaultNav() *Nav { return defaultNav(DefaultAdminPath) }

// entitiesNavFor builds the subnav from the counts the list already loaded
// (schemas arrive ordered by name). active is the resolved type slug, "" for
// the unfiltered list and for an unknown ?type=.
func entitiesNavFor(types []typeTab, active string) []EntityNavEntry {
	return entitiesNavIn(DefaultAdminPath, types, active)
}

// landingPath is where sign-in and setup end under the default prefix.
const landingPath = DefaultAdminPath + "/entities"

// overviewAttention is overviewAttentionFor under the default admin prefix.
func overviewAttention(t totalsJSON, ignoring []agentJSON, never, published int, q url.Values) []ui.AiaAttention {
	return overviewAttentionFor(DefaultAdminPath, t, ignoring, never, published, q)
}

// ThemeFor is the Theme sidebar for role.
func ThemeFor(role string) []SettingsSection { return themeArea.For(role) }

// SettingsFor is the Settings sidebar for role.
func SettingsFor(role string) []SettingsSection { return settingsArea.For(role) }

// For is the area's sidebar for role under the default admin prefix.
func (ar *adminArea) For(role string) []SettingsSection { return ar.forPrefix(DefaultAdminPath, role) }

// areaLocate is areaLocateIn under the default admin prefix.
func areaLocate(path string) (section, tab string) { return areaLocateIn(DefaultAdminPath, path) }

// areaCrumbs is the breadcrumb chain of an area sub-page: the area, the
// section, the tab, then tail. The section and tab collapse into one crumb
// when they share a label (General, Media, Blocks).
func areaCrumbs(tab string, tail ...Crumb) []Crumb {
	return areaCrumbsFor(DefaultAdminPath, tab, tail...)
}

// styleguide is styleguideSamples under the default admin prefix, the form the
// spec-coverage tests read.
var styleguide = styleguideSamples(DefaultAdminPath)

// SafeNext is SafeNextFor under the default admin prefix.
func SafeNext(raw string) string { return SafeNextFor(DefaultAdminPath, raw) }

// presetsView is presetsViewFor under the default admin prefix.
func presetsView(entries []presetEntry, errs map[string]string, post url.Values) presetsData {
	return presetsViewFor(DefaultAdminPath, entries, errs, post)
}

// BuildAssets bundles css in a fixed order (tokens, base, partials/*, pages/*;
// the last two sorted by name), fingerprints it and the js modules, and reads
// the vendored files pinned by vendor.json. It is deterministic: equal inputs
// give equal URLs.
func BuildAssets(adminFS, vendorFS fs.FS) (*Assets, error) {
	return buildAssets(adminFS, vendorFS, DefaultAdminPath)
}

// profileGateExempted is profileGateExemptedIn under the default prefix.
func profileGateExempted(path string) bool { return profileGateExemptedIn(DefaultAdminPath, path) }

// completeProfilePath is the one-time "Complete your profile" step under the
// default prefix; completeProfile is the same under this instance's prefix.
const completeProfilePath = DefaultAdminPath + "/profile/complete"

// updatesHistoryTable is updatesHistoryTableFor under the default admin prefix.
func updatesHistoryTable(rows []UpdatesHistoryRow, loc *time.Location) ui.Table {
	return updatesHistoryTableFor(DefaultAdminPath, rows, loc)
}

// PickBanner is PickBannerFor under the default admin prefix.
func PickBanner(in UpdatesBannerInput, now time.Time) *UpdateBanner {
	return PickBannerFor(DefaultAdminPath, in, now)
}
