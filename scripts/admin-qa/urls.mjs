// The admin URL inventory: every screen the harness visits, on the final URL
// map of v1-23a, with Appearance and Menus moved under /admin/theme. { name, path, mock, legacy, roles, states }
//  - mock:   name of the mockup screen it is compared with (null = unmocked, solo shot)
//  - legacy: the pre-v1-23a path, used when QA_LEGACY=1 (smoke runs on an old build);
//            null means the screen does not exist there
//  - roles:  who may open it (owner always; editor per the role map)
//  - states: extra a11y states, opened by clicking `click` (best effort: a missing
//            trigger is reported as skipped, and fails the run under QA_STRICT=1)
//  - list:   a list screen: the table comes first, create is a header action (UI-D10)
//  - submit: { fill: {selector: value}, check: selector, button: selector } fills the page form and submits it; the screen is then the page it lands on
//  - expect: a selector that must exist on the final page (the screen fails otherwise)
//  - hover:  also check the resting input border darkens on hover (UI-D11)
//  - as:     the screen needs a user other than the role's: "newcomer" is seeded without a
//            profile, for the Complete-your-profile step. Only consistency.mjs visits these.
//  - path tokens like {post} are resolved from list screens (see lib.mjs)
// `auth: false` screens are visited signed out.
const O = ["owner"], OE = ["owner", "editor"];
const rowMenu = { name: "row-menu", click: '[popovertarget^="menu-"]' };
const pm = ".ProseMirror";
const confirm = { name: "confirm", click: "[data-hm-confirm]" };
// The header "New X" opens the create dialog on list screens (UI-D10: list first).
const newDialog = { name: "new-dialog", click: "[data-hm-open]" };

export const screens = [
  // PR-0 admin-component-contracts: the dev styleguide shows every component in every variant (HATAMOTO_DEV=1).
  ...(process.env.HATAMOTO_DEV === "1" ? [{ name: "styleguide", path: "/admin/_styleguide", mock: null, roles: O }] : []),
  // Entities
  { name: "entities", path: "/admin/entities", mock: "content", roles: OE, states: [rowMenu, { name: "filters", click: '[popovertarget$="filters"]' }], primary: true },
  { name: "entities-type", path: "/admin/entities?type=post", mock: null, roles: OE },
  { name: "entity-new", path: "/admin/entities/new", mock: null, roles: OE },
  { name: "people", path: "/admin/people", mock: null, roles: OE, list: true, states: [newDialog] },
  { name: "people-duplicates", path: "/admin/people/duplicates", mock: null, roles: OE }, // the empty state on the seeded site
  { name: "people-new", path: "/admin/people/new", mock: null, roles: OE },
  // The editors: paired with blog-editor and page-editor (run shots with MOCK_SURFACE=planner).
  // A state with `press` clicks `click`, then presses those keys (slash menu, bubble toolbar).
  { name: "entity-edit-post", path: "/admin/entities/{post}/edit", mock: "blog-editor", roles: OE, states: [
    { name: "slash-menu", click: pm, press: "/" },
    { name: "bubble-toolbar", click: pm, press: "Control+a" },
    { name: "more-menu", click: '[popovertarget="be-more"]' },
    { name: "seo-tab", click: "#be-tab-seo" },
  ] },
  // Pages always open in the builder since B10 (the classic section editor and its states are gone).
  { name: "entity-edit-page", path: "/admin/entities/{page}/edit", mock: "page-editor", roles: OE },
  // The builder (QA_PAGE_NAME must name a page that carries a page_doc). States run in order on one
  // page: the first selects a navigator row, so the second finds the inspector's Style tab.
  { name: "builder-shell", path: "/admin/entities/{page}/edit", mock: null, roles: OE, states: [
    { name: "navigator-focus", click: '[data-hm-bs-tree] [role="treeitem"]' },
    { name: "inspector-style", click: '[role="tab"][data-tab="style"]' },
    { name: "more-style-options", click: "details.hm-bs-more-opts > summary" }, // UX-2: margin, Typography and the rest sit under More style options
    { name: "box-model", click: ".hm-bs-ring--margin .hm-bs-ring__t" }, // the Spacing group's margin and padding rings
    { name: "colour-popover-open", click: 'button.hm-bs-swatch[aria-label^="Text colour"]' }, // the site palette popover (Typography is open by default)
    { name: "palette-open", click: '[data-hm-bs-tree] [role="treeitem"]', press: "/" },
    { name: "shortcuts-open", click: "[data-hm-bs-keys]" },
    { name: "navigator-tab", click: '[data-hm-bs-ltab="nav"]' }, // "/" above left the Add tab open and the tree hidden
    { name: "context-menu", click: '[data-hm-bs-tree] [role="treeitem"]', press: "Shift+F10" }, // the element actions menu at the row
    { name: "history-open", click: "[data-hm-bs-history]" },
    { name: "structure-picker", click: "[data-hm-pe-plus]" }, // the layouts under the first "+"
    { name: "seo-drawer", click: "[data-hm-bs-seo]" }, // the SEO slide-over (last: it sits over the canvas; the shared panel, loaded on first open)
  ] },
  { name: "builder-outline", path: "/admin/entities/{page}/edit?outline=1", mock: null, roles: OE },
  { name: "entity-details-post", path: "/admin/entities/{post}", mock: null, roles: OE }, // the details form: its savebar carries Delete (UI-D12)
  { name: "entity-versions", path: "/admin/entities/{post}/versions", mock: null, roles: OE },
  { name: "entity-translations", path: "/admin/entities/{post}/translations", mock: "s4", roles: OE, expect: ".hm-empty" }, // the default seed is single-locale (A6): only the empty state with "Add a locale"; locales-translations.mjs adds locales and drives the real page
  { name: "entity-translation-new", path: "/admin/entities/{post}/translations/new?locale=pt", mock: null, roles: OE, status: 404 }, // the Create copy page: 404 on the single-locale seed (A6); locales-translations.mjs adds locales and drives the real page
  { name: "entity-delete-confirm", path: "/admin/entities/{post}/delete", mock: null, roles: OE },
  // AI analytics
  { name: "analytics-overview", path: "/admin/analytics", mock: null, roles: OE, primary: true },
  { name: "analytics-traffic", path: "/admin/analytics/traffic", mock: null, roles: OE, primary: true },
  { name: "analytics-conversions", path: "/admin/analytics/conversions", mock: null, roles: OE, primary: true },
  { name: "analytics-crawlers", path: "/admin/analytics/crawlers", legacy: "/admin/analytics/crawlers?tab=errors", mock: null, roles: OE, primary: true },
  { name: "analytics-google", path: "/admin/analytics/google", mock: null, roles: OE, primary: true },
  { name: "analytics-content", path: "/admin/analytics/content", mock: null, roles: OE, primary: true },
  { name: "analytics-referrals", path: "/admin/analytics/referrals", legacy: "/admin/analytics/pageviews", mock: null, roles: OE, primary: true },
  { name: "analytics-health", path: "/admin/analytics/health", mock: null, roles: OE, primary: true },
  // Populated only with QA_SEED_ANALYTICS=1; without rows the page still answers 200 with empty cards.
  { name: "analytics-agent", path: "/admin/analytics/agents/gptbot", mock: null, roles: OE },
  // Plugins
  { name: "plugins", path: "/admin/plugins", legacy: "/admin/settings/plugins", mock: "plugins", roles: O, primary: true, states: [newDialog] }, // not a list screen (UI-D10): the header offers Browse and Upload, and the page also carries the update strip and the unsigned banner; the Upload opener is the dialog state
  { name: "plugins-browse", path: "/admin/plugins/browse", mock: null, roles: O, expect: ".hm-callout, .hm-pluginbrowse" }, // the harness has no registry: the "No registry is set up" callout; with one, the search form
  { name: "theme-browse", path: "/admin/theme/browse", mock: null, roles: O, expect: ".hm-callout, .hm-themebrowse" }, // the harness has no registry: the "No registry is set up" callout
  { name: "plugins-security", path: "/admin/plugins/security", mock: null, roles: O, expect: "#signers-card", states: [{ name: "unsigned-open", click: ".hm-pluginsecurity__advanced > summary" }] }, // the allow-unsigned form sits in a closed disclosure
  { name: "settings-plugins-upload", path: "/admin/plugins/upload", mock: null, roles: O },
  // Settings: General, API, Entity models
  { name: "settings-general", path: "/admin/settings/general", legacy: "/admin/settings/site", mock: "settings-general", roles: O, primary: true, hover: true },
  // Settings > Locales. A single-locale seed shows the add-only state.
  { name: "settings-locales", path: "/admin/settings/locales", mock: "s1", roles: O, states: [{ name: "add-locale", click: '[data-hm-open="add-locale"]' }] },
  { name: "settings-locale-addresses", path: "/admin/settings/locales/addresses", mock: "s1", roles: O },
  { name: "settings-locale-remove-confirm", path: "/admin/settings/locales/remove/fr", mock: null, roles: O, status: 404 }, // the default seed is single-locale, so the locale is unknown (404); with fr seeded through Add locale this is the confirm page (200)
  { name: "settings-locale-business", path: "/admin/settings/locales/business", mock: "s1", roles: O },
  { name: "settings-performance", path: "/admin/settings/performance", mock: null, roles: O },
  { name: "settings-updates", path: "/admin/settings/updates", mock: null, roles: O },
  // Pinned by HATAMOTO_ADMIN_PATH in this harness, so the page shows the read-only text. The change and Keep screens
  // need an unpinned install: shots-address.mjs walks them (wizard, change, banner, keep) against a fresh database.
  { name: "settings-security-admin-address", path: "/admin/settings/security/admin-address", legacy: null, mock: null, roles: O },
  { name: "settings-backups", path: "/admin/settings/backups", mock: null, roles: O, states: [rowMenu] }, // rows come from backups-seed.sh; the confirm link lives in the closed row menu, so open the menu instead of clicking it
  { name: "settings-api-keys", path: "/admin/settings/api-keys", legacy: "/admin/settings/api", mock: null, roles: O, primary: true, list: true, states: [newDialog] },
  // submit: fill and submit the /new form so the page shown is the redirect with the one-time secret banner and the highlighted new row.
  { name: "settings-api-keys-secret", path: "/admin/settings/api-keys/new", legacy: null, mock: null, roles: O, submit: { fill: { "#key-name": "QA key" }, check: "#scope-0", button: 'form[action="/admin/settings/api-keys"] button.hm-btn--primary' }, expect: ".hm-secretbanner" },
  { name: "settings-api-keys-new", path: "/admin/settings/api-keys/new", mock: null, roles: O },
  { name: "settings-webhooks", path: "/admin/settings/webhooks", legacy: null, mock: null, roles: O, primary: true, list: true, states: [newDialog] },
  { name: "settings-webhooks-new", path: "/admin/settings/webhooks/new", legacy: null, mock: null, roles: O },
  { name: "settings-webhooks-deliveries", path: "/admin/settings/webhooks/deliveries", legacy: null, mock: null, roles: O }, // read-only log: no create action, so no UI-D10 primary
  { name: "settings-types", path: "/admin/settings/types", legacy: "/admin/schemas", mock: "settings-types", roles: O, primary: true },
  { name: "settings-types-new", path: "/admin/settings/types/new", legacy: "/admin/schemas/new", mock: null, roles: O },
  { name: "settings-types-edit", path: "/admin/settings/types/{type}", mock: null, roles: O },
  { name: "settings-taxonomies", path: "/admin/settings/taxonomies", legacy: "/admin/taxonomies", mock: "settings-taxonomies", roles: O, list: true, states: [newDialog] },
  { name: "settings-taxonomies-new", path: "/admin/settings/taxonomies/new", mock: null, roles: O },
  // Forms
  { name: "settings-forms", path: "/admin/settings/forms", legacy: "/admin/forms", mock: "settings-forms", roles: OE, primary: true },
  { name: "settings-forms-new", path: "/admin/settings/forms/new", legacy: "/admin/forms/new", mock: null, roles: OE },
  { name: "settings-forms-edit", path: "/admin/settings/forms/{form}/edit", legacy: "/admin/forms/{form}/edit", mock: null, roles: OE },
  { name: "settings-submissions", path: "/admin/settings/submissions?form={form}", legacy: "/admin/forms/{form}/submissions", mock: "settings-submissions", roles: OE },
  { name: "settings-submissions-all", path: "/admin/settings/submissions", mock: null, roles: OE }, // no form filter
  // SEO and URLs
  { name: "settings-redirects", path: "/admin/settings/redirects", legacy: "/admin/redirects", mock: "settings-redirects", roles: OE, list: true, states: [newDialog] },
  { name: "settings-redirects-new", path: "/admin/settings/redirects/new", legacy: "/admin/redirects/new", mock: null, roles: OE },
  { name: "settings-redirects-explain", path: "/admin/settings/redirects/explain", mock: null, roles: OE },
  { name: "settings-redirects-404", path: "/admin/settings/redirects/404", legacy: "/admin/redirects/404", mock: null, roles: OE },
  { name: "settings-linking-rule-new", path: "/admin/settings/linking/rules/new", mock: null, roles: OE },
  { name: "settings-linking-suggestions", path: "/admin/settings/linking/suggestions", mock: null, roles: OE },
  { name: "settings-linking-equity", path: "/admin/settings/linking/equity", mock: null, roles: OE },
  { name: "settings-linking-import", path: "/admin/settings/linking/import", mock: null, roles: OE },
  { name: "settings-linking", path: "/admin/settings/linking", legacy: "/admin/linking", mock: "settings-linking", roles: OE },
  // Theme: Themes, Colours and fonts, Design system, Style presets, Layout parts, Blocks, Templates, Menus
  { name: "theme-themes", path: "/admin/theme/themes", legacy: "/admin/settings/theme", mock: "settings-themes", roles: O, primary: true, list: true, states: [newDialog] },
  { name: "theme-colours", path: "/admin/theme/colours", legacy: "/admin/settings/branding", mock: null, roles: O },
  { name: "theme-design-system", path: "/admin/theme/design-system", legacy: null, mock: null, roles: O },
  { name: "theme-presets", path: "/admin/theme/presets", legacy: null, mock: null, roles: O },
  { name: "theme-parts", path: "/admin/theme/parts", legacy: null, mock: null, roles: O },
  // Blocks: New block is a POST to the draft route (a form button), not a link to a create page, so this is not a UI-D10 list screen.
  { name: "theme-blocks", path: "/admin/theme/blocks", legacy: null, mock: null, roles: OE },
  { name: "theme-templates", path: "/admin/theme/templates", legacy: "/admin/sections", mock: "settings-sections", roles: OE },
  { name: "theme-menus", path: "/admin/theme/menus", legacy: "/admin/menus", mock: "settings-menus", roles: OE, list: true, states: [newDialog] },
  { name: "theme-menus-new", path: "/admin/theme/menus/new", mock: null, roles: OE },
  { name: "theme-menus-edit", path: "/admin/theme/menus/{menu}", legacy: "/admin/menus/{menu}", mock: "settings-menus", roles: OE },
  // Team, media, import and export
  { name: "settings-users", path: "/admin/settings/users", legacy: "/admin/users", mock: "settings-users", roles: O, list: true, states: [newDialog] },
  { name: "settings-users-new", path: "/admin/settings/users/new", legacy: "/admin/users/new", mock: null, roles: O },
  { name: "settings-users-invite", path: "/admin/settings/users/invite", mock: null, roles: O },
  { name: "settings-media", path: "/admin/settings/media", legacy: "/admin/media", mock: "media", roles: OE, primary: true, list: true, states: [newDialog] },
  { name: "settings-media-new", path: "/admin/settings/media/new", mock: null, roles: OE },
  { name: "media-detail", path: "/admin/media/{media}", mock: null, roles: OE, optional: true },
  { name: "settings-email", path: "/admin/settings/email", mock: null, roles: O },
  { name: "settings-spam", path: "/admin/settings/spam", mock: null, roles: O },
  { name: "settings-seo-general", path: "/admin/settings/seo/general", mock: null, roles: O },
  { name: "settings-seo-titles", path: "/admin/settings/seo/titles", mock: null, roles: O },
  { name: "settings-import", path: "/admin/settings/import", legacy: "/admin/import", mock: "settings-import", roles: O },
  { name: "settings-export", path: "/admin/settings/export", legacy: null, mock: "settings-export", roles: O },
  // Account, errors, signed-out
  { name: "profile", path: "/admin/profile", mock: null, roles: OE },
  { name: "profile-complete", path: "/admin/profile/complete", mock: null, roles: O, as: "newcomer", expect: 'form[action="/admin/profile/complete"]' },
  { name: "error-404", path: "/admin/this-page-does-not-exist", mock: null, roles: OE, status: 404 },
  { name: "login", path: "/admin/login", mock: null, roles: OE, auth: false },
  { name: "forgot", path: "/admin/forgot", mock: null, roles: OE, auth: false },
  // PR-E admin-media-forms: the media table view and the status-filtered submissions page.
  { name: "settings-media-list", path: "/admin/settings/media?view=list", mock: null, roles: OE },
  { name: "settings-submissions-new", path: "/admin/settings/submissions?form={form}&status=new", mock: null, roles: OE },
];

// Not inventoried because the QA seed cannot reach them without external input:
//  - import options (/admin/settings/import/wordpress/{id}/options) needs a finished
//    WordPress scan, i.e. a WordPress export or site to upload;
//  - plugin settings (/admin/plugins/{name}/settings) needs an installed plugin, and
//    the seeded instance has none. The static lint (UI-01..06) still covers both templates.

// Mockup screens with no counterpart in a signed-in admin (setup wizard, kit page)
// stay out of the pairing on purpose: onboarding, components.

// PR-G admin-linking-redirects: states the seed can reach. Populated 404, equity and
// import screens are covered by the Go fixtures (the seed cannot upload a WordPress export).
screens.push(
  { name: "settings-redirects-explain-result", path: "/admin/settings/redirects/explain?path=/qa-moved", mock: null, roles: OE, expect: ".hm-callout" },
  { name: "settings-linking-suggestions-page", path: "/admin/settings/linking/suggestions?entity={page}", mock: null, roles: OE },
  { name: "settings-linking-inbound", path: "/admin/settings/linking/equity/{page}", mock: null, roles: OE },
);

// PR-C admin-editors: the status tab row, the builder's overflow menu and the post
// editor's drawer opener. The phone layouts (builder pane tabs, settings drawer) are
// checked at 390px by consistency.mjs on the screens above; the bulk delete confirm and a
// populated compare page are POST or multi-version states the seed cannot reach (Go fixtures cover them).
screens.push(
  { name: "entities-status-draft", path: "/admin/entities?status=draft", mock: null, roles: OE },
  { name: "builder-menu", path: "/admin/entities/{page}/edit", mock: null, roles: OE, states: [{ name: "more-menu", click: '[popovertarget="bs-more"]' }] },
);
// PR-D admin-settings: confirm pages the seed can reach. The plugin install prompt needs an uploaded
// package and the backup delete confirm needs a backup id, so those stay with the Go fixtures.
screens.push(
  { name: "settings-theme-activate", path: "/admin/theme/themes/default/activate", legacy: null, mock: null, roles: O },
);
// PR-F admin-shell-auth: states the seed can reach. The setup wizard, accept/reset dead ends
// and the invite link page need fixtures the seed cannot make; the Go golden tests cover them.
screens.push(
  { name: "settings-users-new-prefill", path: "/admin/settings/users/new?email=ann@x.test", mock: null, roles: O, expect: 'input[value="ann@x.test"]' },
  { name: "error-403-editor", path: "/admin/settings/users", mock: null, roles: ["editor"], status: 403 },
);

// PR-H admin-dashboard: the Overview. Analytics cards are populated with QA_SEED_ANALYTICS=1; seed.sh seeds 404 hits and submissions, so those cards are populated (the Go goldens cover the empty and error states).
screens.push(
  { name: "dashboard", path: "/admin", mock: null, roles: OE, expect: ".hm-dash", primary: true },
  // Owner-only, read-only list: no create action, so no UI-D10 primary. Present only when the audit reader is wired (server always wires it with a database).
  { name: "audit-log", path: "/admin/audit", mock: null, roles: O, expect: ".hm-table" },
);

import { role } from "./lib.mjs";
const legacy = process.env.QA_LEGACY === "1";

// inventory returns the screens to visit for the current role (and path set).
export function inventory({ only } = {}) {
  return screens
    .filter((s) => s.roles.includes(role))
    .filter((s) => !s.as)
    .filter((s) => !legacy || s.legacy !== null)
    .filter((s) => !only?.length || only.includes(s.name))
    .map((s) => ({ ...s, path: legacy && s.legacy ? s.legacy : s.path }));
}
export const primaryScreens = () => inventory().filter((s) => s.primary);

// Reachable /admin paths the crawl (crawl.mjs) must not report, keyed by the normalised path
// (ids are {id}, only type|tab|form stay in the query). Each entry needs a reason.
// error-404 is deliberately unknown and is never reached by a link, so it needs no entry.
export const crawlIgnore = [
  { re: /^\/admin\/plugins\/[^/?]+\/settings$/, reason: "plugin settings need an installed plugin; the seeded instance has none (see Not inventoried above)" },
  { re: /^\/admin\/settings\/import\/wordpress\/[^/?]+\/options$/, reason: "import options need a finished WordPress scan (see Not inventoried above)" },
  { re: /^\/admin\/plugins\/[^/?]+\/about$/, reason: "the plugin detail page needs a registry entry; the harness has no registry" },
];
