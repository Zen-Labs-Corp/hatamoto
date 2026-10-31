package designcheck

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/web"
)

// Static admin UI rules UI-01 to UI-11, documented in docs/ADMIN_UI_RULES.md.
// Every violation prints as "file:line UI-0N message".

type uiViolation struct {
	File string
	Line int
	Rule string
	Msg  string
	Text string // the offending source line, matched against allowlist needles
}

func (v uiViolation) String() string {
	return fmt.Sprintf("%s:%d %s %s", v.File, v.Line, v.Rule, v.Msg)
}

// uiAllowEntry names one place that may break a rule. Needle must occur on the
// offending source line and must identify the element (an id, a label, a data
// hook), never the rule's own trigger. An entry with no Reason, one that
// matches nothing (stale), one whose needle is a bare trigger, or one that
// matches more than uiAllowMaxLines lines fails the test, so an entry cannot
// quietly exempt a whole file.
type uiAllowEntry struct {
	Rule, File, Needle, Reason string
	Max                        int // lines the entry may cover; 0 means uiAllowMaxLines, never above uiAllowHardMax
}

// uiAllowMaxLines is how many source lines one allowlist entry may cover.
const (
	uiAllowMaxLines = 3
	uiAllowHardMax  = 10 // the most any entry may declare with Max
)

func (a uiAllowEntry) maxLines() int {
	if a.Max > 0 {
		return min(a.Max, uiAllowHardMax)
	}
	return uiAllowMaxLines
}

// uiBareTriggers are needles that every violation of a rule contains by
// definition. They are compared after trimming `class=`, quotes and spaces.
var uiBareTriggers = map[string]bool{
	"hm-btn": true, "hm-card": true, "hm-savebar": true, "hm-card__foot": true,
	"button": true, "form": true, "input": true, "checkbox": true, "submit": true,
	`type="checkbox"`: true, `type="submit"`: true, "box-shadow": true, "class": true,
	"hm/button": true, "hm/card-start": true, "hm-badge": true, "hm/badge": true,
}

func bareTrigger(needle string) bool {
	n := strings.TrimSpace(needle)
	for _, p := range []string{"class=", "<"} {
		n = strings.TrimPrefix(n, p)
	}
	n = strings.Trim(n, "\"'` ")
	return n == "" || uiBareTriggers[n]
}

const (
	reasonEditor  = "editor toolbar or canvas control, not a form action"
	reasonConsent = "consent gate, an acknowledgement and not a setting"
	reasonMulti   = "multi-pick list, several boxes can be ticked"
)

var uiAllow = []uiAllowEntry{
	// UI-01: submits that are row, setting or inline-add actions, not a form's save action.
	// Also the only home of POST forms that used to skip the rule by class name (hm-inline)
	// and of partial forms that were skipped as "partials".
	{Rule: "UI-01", File: "pages/settings/performance.html", Needle: `"Preload now"`, Reason: "setting row action bound to its own form by the form attribute"},
	{Rule: "UI-01", File: "pages/settings/email.html", Needle: `"Send a test email"`, Reason: "setting action bound to its own form by the form attribute, so it tests the saved settings without posting the form"},
	{Rule: "UI-01", File: "pages/settings/email.html", Needle: `instead"`, Reason: "setting action bound to its own form by the form attribute, so it clears the saved settings without posting the form"},
	{Rule: "UI-01", File: "pages/settings/layoutparts.html", Needle: `printf "Create %s part"`, Reason: "one create button per slot, each in its own one-button form in a row"},
	{Rule: "UI-01", File: "pages/entities/new.html", Needle: `<button class="hm-choice__link" type="submit">`, Reason: "a type card is its own one-button form: one click creates the draft, as the Create menu does"},
	{Rule: "UI-01", File: "pages/settings/updates.html", Needle: `"Check now"`, Reason: "setting row action bound to its own form by the form attribute"},
	{Rule: "UI-01", File: "pages/settings/updates.html", Needle: `"Unpin"`, Reason: "setting row action bound to its own form by the form attribute"},
	{Rule: "UI-01", File: "pages/settings/updates.html", Needle: `"Reset ID"`, Reason: "setting row action bound to its own form by the form attribute"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `"Turn off now"`, Reason: "banner action in its own form: turns the opt-in off at once"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `Update all (`, Reason: "list action above the plugin list, a form of its own that updates every plugin without a review step"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `"Update to v%s"`, Reason: "list row action in a plugin row"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `"Review update to v%s"`, Reason: "list row action in a plugin row"},
	{Rule: "UI-01", File: "pages/plugins/browse.html", Needle: `"Try again"`, Reason: "retry action of the unreachable-registry state, a form of its own"},
	{Rule: "UI-01", File: "pages/theme/browse-detail.html", Needle: `data-hm-panel-actions`, Reason: "drawer actions: pluginpanel.js moves this block into the dialog footer; the one-button POST form is the primary action"},
	{Rule: "UI-01", File: "pages/theme/browse.html", Needle: `"Try again"`, Reason: "retry action of the unreachable-registry state, a form of its own"},
	{Rule: "UI-01", File: "pages/entities/compare.html", Needle: `.RestoreLabel`, Reason: "list row action: restores one node of the layout, a form of its own in the changes table"},
	{Rule: "UI-01", File: "pages/media/picker.html", Needle: `"Upload"`, Reason: "inline upload beside its file input in a dialog"},
	{Rule: "UI-01", File: "pages/forms/edit.html", Needle: `"Add field"`, Reason: "inline add row inside the editor, next to its select"},
	{Rule: "UI-01", File: "pages/schemas/edit.html", Needle: `"Add field"`, Reason: "inline add row inside the editor, next to its select"},
	{Rule: "UI-01", File: "pages/schemas/edit.html", Needle: `value="up:{{$f.Key}}"`, Reason: "field row operations (move, remove) inside the editor"},
	{Rule: "UI-01", File: "pages/schemas/edit.html", Needle: `save_json`, Reason: "action of the advanced JSON disclosure, a nested form of its own"},
	{Rule: "UI-01", File: "pages/menus/edit.html", Needle: `"Add item"`, Reason: "inline add row next to its select"},
	{Rule: "UI-01", File: "pages/setup/done.html", Needle: `"Send me a test email"`, Reason: "one inline row: the email note and its test button, not a form footer"},
	{Rule: "UI-01", File: "layouts/shell.html", Needle: `"Label" "No thanks"`, Reason: "answer in the owner's install stats banner: one inline row of text and two buttons, not a form footer"},
	{Rule: "UI-01", File: "layouts/shell.html", Needle: `"Label" "Send stats"`, Reason: "answer in the owner's install stats banner: one inline row of text and two buttons, not a form footer"},
	{Rule: "UI-01", File: "partials/segmented.html", Needle: `class="hm-segmented__btn"`, Reason: "segmented control: each button posts its own value, the control is the footer-less form"},
	{Rule: "UI-01", File: "pages/linking/suggestions.html", Needle: `suggestions/insert`, Reason: "one-button POST form in a table row (was exempt by hm-inline)"},
	{Rule: "UI-01", File: "partials/builderoutline.html", Needle: `"Add to the end"`, Reason: "inline add row in the builder outline, next to its select"},
	{Rule: "UI-01", File: "partials/pageheader.html", Needle: `{{range .Items}}{{if .Post}}<form`, Reason: "menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "partials/pageheader.html", Needle: `{{with .Primary}}`, Reason: "one-button POST form in the page header"},
	{Rule: "UI-01", File: "partials/pluginhead.html", Needle: `{{if .Enabled}}disable`, Reason: "one-button POST form (Turn on or off) in the plugin header row"},
	{Rule: "UI-01", File: "partials/sectionhead.html", Needle: `{{with $.Primary}}`, Reason: "one-button POST form in the section header"},
	{Rule: "UI-01", File: "partials/pageheader.html", Needle: `id="hm-overflow"`, Reason: "overflow menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "partials/rowmenu.html", Needle: `{{$a.Post}}`, Reason: "row menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "partials/sidebar.html", Needle: `action="{{adminPath}}/theme"`, Reason: "theme toggle in the sidebar"},
	{Rule: "UI-01", File: "partials/sidebar.html", Needle: `action="{{adminPath}}/logout"`, Reason: "sign out button in the sidebar"},
	{Rule: "UI-01", File: "partials/table.html", Needle: `data-hm-bulk-toolbar`, Reason: "bulk action toolbar above a table, not a form footer"},
	{Rule: "UI-01", File: "partials/mediatile.html", Needle: `data-hm-bulk-toolbar`, Reason: "bulk action toolbar above a media grid, not a form footer"},

	// UI-10: page-level forms that edit or remove something, not a create form beside the list.
	{Rule: "UI-10", File: "pages/linking/rules.html", Needle: `rules/delete-source`, Reason: "removes a set of rules by source, a destructive action and not a create form"},
	{Rule: "UI-10", File: "pages/settings/theme.html", Needle: `Update to %s`, Reason: "per-card Update button of an installed theme, a row action and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `action="{{adminPath}}/settings/updates" class`, Reason: "the update policy form, a settings form and not a create form; the history table below is read-only"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="check-form"`, Reason: "Check now, a row action of the status card and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="nobackup-form"`, Reason: "Update without a backup, a row action of the status card that opens a confirm page and is not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="apply-form"`, Reason: "Update now, a row action of the status card and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="unpin-form"`, Reason: "Unpin, a row action of the policy card and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="stats-form"`, Reason: "the Install stats switch, a settings form and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="reset-stats-form"`, Reason: "Reset ID, a row action of the Install stats card and not a create form"},
	{Rule: "UI-10", File: "pages/settings/backups.html", Needle: `id="backup-form"`, Reason: "Back up now, a header action that starts a background job and takes no input, so there is nothing to put in a dialog"},

	// UI-02: selectors that name the class, not markup that uses it.
	{Rule: "UI-02", File: "css/base.css", Needle: "html:has(.hm-savebar)", Reason: "reserves scroll space when a sticky footer exists"},

	// UI-03: checkboxes that are not a lone on/off setting.
	{Rule: "UI-03", File: "partials/filterbar.html", Needle: `"Control" "checkbox"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/settings/packageconfirm.html", Needle: `"confirm"`, Reason: reasonConsent},
	{Rule: "UI-03", File: "pages/settings/pluginprompt.html", Needle: `"confirm"`, Reason: reasonConsent},
	{Rule: "UI-03", File: "pages/settings/spam.html", Needle: `"Name" "scripts"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/settings/pluginprompt.html", Needle: `name="permission"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/plugins/permissions.html", Needle: `name="permission"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "partials/form-apikey.html", Needle: `"scopes"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "partials/form-webhook.html", Needle: `"events"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/import/options.html", Needle: `.Types`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/entities/edit.html", Needle: `name="term"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/entities/blogedit.html", Needle: `name="term"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "partials/table.html", Needle: `data-hm-select-all`, Reason: "table select-all box"},
	{Rule: "UI-03", File: "partials/table.html", Needle: `data-hm-row-select`, Reason: "table row select box"},
	{Rule: "UI-03", File: "partials/mediatile.html", Needle: `name="id"`, Reason: "media tile select box"},
	{Rule: "UI-03", File: "schemaform/field.html", Needle: `__clear`, Reason: "clear the stored value, a destructive tick and not a setting"},

	// UI-04: shadows outside the popup list.
	{Rule: "UI-04", File: "css/partials/chart.css", Needle: ".hm-statcard { box-shadow", Reason: "stat cards carry the lightest shadow; the one named exception to the no-shadow card rule"},
	{Rule: "UI-04", File: "css/pages/blogeditor.css", Needle: ".hm-ed__bar", Reason: "sticky blog editor toolbar; editor_polish_test.go asserts blogeditor.css"},

	// UI-05: markup that cannot go through hm/button or hm/card-start. Needles name
	// the element by a data hook, id, label or variable, never by hm-btn or hm-card.
	{Rule: "UI-05", File: "partials/sidebar.html", Needle: "data-hm-theme-toggle", Reason: "theme toggle: one button, two icons swapped by CSS"},
	{Rule: "UI-05", File: "partials/filterbar.html", Needle: `-filters"`, Reason: "filters button with a live count badge"},
	{Rule: "UI-05", File: "partials/daterange.html", Needle: "hm-daterange__button", Reason: "needs hm-daterange__button; analytics_test.go asserts the string"},
	{Rule: "UI-05", File: "partials/pageheader.html", Needle: "{{with .Menu}}<button", Reason: "menu trigger: icon, label and chevron in one primary button"},
	{Rule: "UI-05", File: "pages/menus/edit.html", Needle: "data-hm-op=", Max: 3, Reason: "menu tree icon buttons whose hidden text is rewritten by menutree.js"},
	{Rule: "UI-05", File: "pages/schemas/edit.html", Needle: `value="up:{{$f.Key}}"`, Reason: "row operations with a visually hidden suffix naming the field"},
	{Rule: "UI-05", File: "schemaform/field.html", Needle: "$.Orderable", Reason: "repeater row operations with the item number in the label"},
	{Rule: "UI-05", File: "schemaform/field.html", Needle: `_add.{{.Name}}`, Reason: "repeater add button with the field label"},
	{Rule: "UI-05", File: "schemaform/field.html", Needle: "data-hm-media-picker", Reason: "hidden-until-JS media picker button"},
	{Rule: "UI-05", File: "pages/linking/suggestions.html", Needle: "suggestions/insert", Reason: "row action with a visually hidden suffix naming the phrase"},
	{Rule: "UI-05", File: "js/blogeditor.js", Needle: "over.className", Reason: reasonEditor},
	{Rule: "UI-05", File: "js/blogeditor.js", Needle: "reload.className", Reason: reasonEditor},
	{Rule: "UI-05", File: "js/blogeditor.js", Needle: "btn.className", Reason: reasonEditor},
	{Rule: "UI-05", File: "js/blogeditor.js", Needle: "data-hm-links-refresh", Reason: reasonEditor},
	{Rule: "UI-05", File: "js/builder/keys.js", Needle: `"Close"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/builder/navigator.js", Needle: `"Add a section"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/canvas/host.js", Needle: `"Dismiss"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/htmlblocks.js", Needle: `"Edit HTML"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/kit.js", Needle: `text: "Apply"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/kit.js", Needle: `text: "Cancel"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/kit.js", Needle: `text: "Remove"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/content.js", Needle: "data-hm-picker-close", Reason: "media picker dialog buttons built in JS"},
	{Rule: "UI-05", File: "pages/settings/theme.html", Needle: `id="theme-{{.Name}}"`, Reason: "theme card with a dynamic id, heading in the body row and an Active modifier"},
	{Rule: "UI-05", File: "pages/forms/submission.html", Needle: `aria-labelledby="sub-answers"`, Reason: "card whose header carries a status badge"},
	{Rule: "UI-05", File: "pages/redirects/list.html", Needle: "hm-redirect-test", Reason: "inline test form styled as a card"},
	{Rule: "UI-05", File: "pages/entities/versions.html", Needle: "hm-compare", Reason: "inline compare form styled as a card"},
	{Rule: "UI-05", File: "pages/entities/compare.html", Needle: "hm-tablecard", Reason: "table card, no head or body"},
	{Rule: "UI-05", File: "pages/entities/version.html", Needle: "hm-tablecard", Reason: "table card, no head or body"},
	{Rule: "UI-05", File: "pages/entities/new.html", Needle: "hm-choice", Reason: "choice tile that is a list item"},
	{Rule: "UI-05", File: "pages/media/detail.html", Needle: "hm-media-detail__preview", Reason: "image preview tile, no head or body"},
	{Rule: "UI-05", File: "pages/schemas/edit.html", Needle: `id="advanced"`, Reason: "details disclosure styled as a card"},
	{Rule: "UI-05", File: "pages/linking/rules.html", Needle: `id="clean-up"`, Reason: "details disclosure styled as a card"},

	{Rule: "UI-04", File: "css/pages/pageeditor.css", Needle: ".hm-pe__fmt {", Reason: "the canvas formatting bar floats over the page like a popup"},
	{Rule: "UI-04", File: "css/pages/pageeditor.css", Needle: ".hm-pe__toolbar {", Reason: "the canvas element toolbar floats over the page like a popup"},
	// UI-06: editor and chip scope.
	{Rule: "UI-06", File: "css/partials/danger-confirm.css", Needle: ".hm-danger-confirm__card .hm-card__title", Reason: "danger title colour like .hm-dialog--danger; hm/card-start has no title class hook"},
	{Rule: "UI-06", File: "css/pages/blogeditor.css", Needle: ".hm-be__side .hm-card", Max: 4, Reason: "blog editor side panel; editor_polish_test.go asserts blogeditor.css"},
	{Rule: "UI-06", File: "css/pages/blogeditor.css", Needle: ".hm-field:has(> .hm-ed--blog)", Reason: "blog editor label chrome; editor_polish_test.go asserts blogeditor.css"},
	{Rule: "UI-06", File: "css/pages/content.css", Needle: ".hm-entities .hm-table th", Max: 2, Reason: "the entities list packs its rows tighter than the default table (padding only, on wide screens)"},
	{Rule: "UI-06", File: "css/pages/content.css", Needle: `input[type="file"].hm-field__control`, Reason: "native file input needs its own box"},
	{Rule: "UI-06", File: "css/partials/settings.css", Needle: `.hm-fieldgrid--tokens .hm-field__control:not([type="color"])`, Reason: "design token inputs are compact so the page is not four screens tall; colour inputs keep their own box"},
	{Rule: "UI-06", File: "css/partials/steps.css", Needle: `.hm-field__control[type="color"]`, Max: 2, Reason: "native colour input needs its own box"},
	{Rule: "UI-05", File: "js/canvas/fmtbar.js", Needle: `class: "hm-btn`, Max: 3, Reason: reasonEditor},
	{Rule: "UI-06", File: "css/partials/listbox.css", Needle: ".hm-bubble .hm-btn", Reason: "editor bubble buttons"},
	{Rule: "UI-06", File: "css/pages/blogeditor.css", Needle: ".hm-be__links > .hm-dialog__foot", Reason: "the links dialog is a flex column with a scrolling body; its footer pads to the list's inset and keeps a hairline above it (TestBlogEditorDialogLayout)"},
	{Rule: "UI-06", File: "css/pages/menus.css", Needle: ".hm-menutree .hm-menu", Reason: "disabled Indent/Outdent in the menu tree must not look clickable (TestMenuTreeStylesDisabledMenuItems); follow-up: a :disabled state in menu.css for every menu"},
	{Rule: "UI-06", File: "css/partials/popover.css", Needle: ".hm-btn .hm-btn-count", Reason: "count badge inside the filters button"},
	{Rule: "UI-06", File: "css/partials/schemaform.css", Needle: ".hm-repeater > .hm-btn", Reason: "repeater Add button carries the long schema title and must wrap in the narrow builder inspector"},

	// UI-07: multi-submit forms whose first submit is not the primary action.
	{Rule: "UI-07", File: "partials/table.html", Needle: `data-hm-bulk-toolbar`, Reason: "bulk toolbar holds no text field, so Enter cannot submit it; every button runs only on click"},
}

// uiPopupSelectors may carry --shadow-2 or --shadow-3. Everything else may
// only use none, a ring or an inset shadow.
var uiPopupSelectors = []string{
	".hm-dialog", ".hm-menu", ".hm-popover-panel", ".hm-slash", ".hm-bubble",
	".hm-toast", ".hm-bs-picker", ".hm-bs-history", ".hm-searchselect__list", ".hm-select__list", ".hm-tokens__list", ".hm-pe__rich",
}

// uiPopupRe matches whole popup class tokens (with __elem or --mod), so
// .hm-menu does not match .hm-menutree.
var uiPopupRe = func() *regexp.Regexp {
	var alts []string
	for _, o := range uiPopupSelectors {
		alts = append(alts, regexp.QuoteMeta(o))
	}
	return regexp.MustCompile(`(?:` + strings.Join(alts, "|") + `)(?:(?:__|--)[\w-]+)?(?:[^\w-]|$)`)
}()

var (
	uiFormOpen     = regexp.MustCompile(`<form\b[^>]*>`)
	uiFormID       = regexp.MustCompile(`\bid="([^"]+)"`)
	uiButtonTag    = regexp.MustCompile(`<button\b[^>]*>`)
	uiInputSubmit  = regexp.MustCompile(`<input\b[^>]*type="submit"[^>]*>`)
	uiButtonCall   = regexp.MustCompile(`(?s)\{\{template "hm/button"[^}]*\}\}`)
	uiPrimaryCall  = regexp.MustCompile(`"Variant" "(primary|danger)"`)
	uiPrimaryRaw   = regexp.MustCompile(`hm-btn--(primary|danger)([^\w-]|$)`)
	uiSavebarCls   = regexp.MustCompile(`(hm-savebar|hm-card__foot)(?:--plain)?([^\w-]|$)`)
	uiCheckboxTag  = regexp.MustCompile(`<input\b[^>]*type="checkbox"[^>]*>`)
	uiCheckboxCtl  = regexp.MustCompile(`"Control" "checkbox"`)
	uiClassAttr    = regexp.MustCompile(`class\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'>]+)`)
	uiMethodAttr   = regexp.MustCompile(`\bmethod\s*=\s*"([^"]*)"`)
	uiHTMXVerb     = regexp.MustCompile(`\bhx-(?:post|put|patch|delete)\b`)
	uiShadowDecl   = regexp.MustCompile(`box-shadow\s*:\s*([^;}]*)`)
	uiBannedFx     = regexp.MustCompile(`drop-shadow\(|text-shadow`)
	uiJSToken      = regexp.MustCompile(`(^|[^\w.-])hm-(btn|card|badge)($|[^\w-])`)
	uiCSSRule      = regexp.MustCompile(`(?s)([^{}]+)\{`)
	uiSimplePseudo = regexp.MustCompile(`::?[\w-]+(?:\([^().#\[\]]*\))?`)
	uiAsideOpen    = regexp.MustCompile(`<span class="[^"]*(?:hm-card__foot-note|hm-savebar__aside)[^"]*">`)
	uiRestyleProp  = regexp.MustCompile(`(?:^|[{;\s])(?:background[\w-]*|border[\w-]*|color|box-shadow|font[\w-]*|height|min-height|padding[\w-]*|text-transform|opacity|outline[\w-]*)\s*:`)
	uiTemplateAct  = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
)

// Pages that keep the sticky flat footer (UI-02).
var uiStickyTemplates = map[string]bool{
	"pages/entities/edit.html":   true,
	"pages/schemas/edit.html":    true,
	"pages/forms/edit.html":      true,
	"pages/taxonomies/term.html": true,
	"pages/plugins/plugin.html":  true,
	// The menu editor: Save, Discard and the unsaved state stay in reach beside a long tree.
	"pages/menus/edit.html": true,
	// The robots policy form's pending-changes bar, shared by Crawlers, Health and the agent page.
	"partials/aia-policy.html": true,
}

// Component partials are the component: they may spell their own classes.
var uiComponentPartials = map[string]bool{
	"partials/button.html": true,
	"partials/card.html":   true,
	"partials/table.html":  true,
	"partials/stat.html":   true,
	"partials/badge.html":  true,
}

// Component ownership comes from components.json, the one registry: every
// class block in a component's base selector (.hm-field__control -> hm-field)
// belongs to that component's owner file, which may style it any way it likes
// (UI-06). uiComponent matches a whole block token; group 3 is non-empty when
// the match is only a prefix (.hm-btn-count), which is not the component.
// uiLoneComp is one block class with an optional element or modifier.
var (
	uiCSSOwner  = uiOwnersFromSpec()
	uiComponent = regexp.MustCompile(`\.(` + uiOwnerAlt(uiCSSOwner) + `)((?:__|--)[\w-]*)?([\w-]*)`)
	uiLoneComp  = regexp.MustCompile(`^\.(` + uiOwnerAlt(uiCSSOwner) + `)(?:(?:__|--)[\w-]+)?$`)
)

var uiSpecBlock = regexp.MustCompile(`\.(hm-[a-z0-9]+(?:-[a-z0-9]+)*)`)

func uiOwnersFromSpec() map[string]string {
	spec, err := loadComponents(web.Admin())
	if err != nil {
		panic(err)
	}
	owners := map[string]string{}
	for _, name := range sortedNames(spec) {
		c := spec[name]
		for _, m := range uiSpecBlock.FindAllStringSubmatch(c.Selector, -1) {
			if o, ok := owners[m[1]]; ok && o != c.Owner {
				panic(fmt.Sprintf("components.json: .%s is owned by both %s and %s", m[1], o, c.Owner))
			}
			owners[m[1]] = c.Owner
		}
	}
	return owners
}

// uiOwnerAlt is the owned blocks as a regexp alternation, longest first so a
// block never loses to a shorter one it starts with.
func uiOwnerAlt(owners map[string]string) string {
	blocks := make([]string, 0, len(owners))
	for b := range owners {
		blocks = append(blocks, regexp.QuoteMeta(b))
	}
	sort.Slice(blocks, func(i, j int) bool {
		if len(blocks[i]) != len(blocks[j]) {
			return len(blocks[i]) > len(blocks[j])
		}
		return blocks[i] < blocks[j]
	})
	return strings.Join(blocks, "|")
}

type uiFile struct {
	name string
	src  string
}

var uiCSSComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// blankComments keeps offsets and line numbers while hiding comment text.
func blankComments(src string) string {
	return uiCSSComment.ReplaceAllStringFunc(src, func(c string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, c)
	})
}

func lineOf(src string, off int) int { return strings.Count(src[:off], "\n") + 1 }

func lineText(src string, off int) string {
	s := strings.LastIndex(src[:off], "\n") + 1
	e := strings.Index(src[off:], "\n")
	if e < 0 {
		return src[s:]
	}
	return src[s : off+e]
}

func readUIFiles(t *testing.T, fsys fs.FS) []uiFile {
	t.Helper()
	var out []uiFile
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".html") && !strings.HasSuffix(p, ".css") && !strings.HasSuffix(p, ".js") {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out = append(out, uiFile{p, string(b)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// lintAdmin runs UI-01 to UI-11 over every html, css and js file in fsys.
func lintAdmin(t *testing.T, fsys fs.FS) []uiViolation {
	t.Helper()
	var vs []uiViolation
	for _, f := range readUIFiles(t, fsys) {
		add := func(off int, rule, msg string) {
			vs = append(vs, uiViolation{f.name, lineOf(f.src, off), rule, msg, lineText(f.src, off)})
		}
		switch {
		case strings.HasSuffix(f.name, ".html"):
			lintUI01(f, add)
			lintUI07(f, add)
			lintUI02(f, add)
			lintUI03(f, add)
			lintUI05HTML(f, add)
			lintUI08(f, add)
			lintUI09(f, add)
			lintUI10(f, add)
		case strings.HasSuffix(f.name, ".css"):
			f.src = blankComments(f.src)
			lintUI02CSS(f, add)
			lintUI04(f, add)
			lintUI06(f, add)
			lintUI11(f, add)
		case strings.HasSuffix(f.name, ".js"):
			lintUI05JS(f, add)
		}
	}
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].File != vs[j].File {
			return vs[i].File < vs[j].File
		}
		return vs[i].Line < vs[j].Line
	})
	return vs
}

type uiSpan struct{ start, end int }

func (s uiSpan) has(p int) bool { return p >= s.start && p < s.end }

// footerSpans finds every footer region in a template.
func footerSpans(src string) []uiSpan {
	var out []uiSpan
	pair := func(open, close string) {
		for from := 0; ; {
			i := strings.Index(src[from:], open)
			if i < 0 {
				return
			}
			i += from
			j := strings.Index(src[i:], close)
			if j < 0 {
				out = append(out, uiSpan{i, len(src)})
				return
			}
			out = append(out, uiSpan{i, i + j})
			from = i + j
		}
	}
	pair(`{{template "hm/card-foot"}}`, `{{template "hm/card-foot-end"}}`)
	pair(`{{template "hm/foot"}}`, `{{template "hm/foot-end"}}`)
	pair(`{{template "hm/dialog-end"`, `}}`)
	for _, m := range regexp.MustCompile(`class="hm-savebar"|hm-dialog__foot`).FindAllStringIndex(src, -1) {
		end := strings.Index(src[m[1]:], "</div>")
		if f := strings.Index(src[m[1]:], "</form>"); f >= 0 && (end < 0 || f < end) {
			end = f
		}
		if end < 0 {
			end = len(src) - m[1]
		}
		out = append(out, uiSpan{m[0], m[1] + end})
	}
	return out
}

// uiNoSubmitRule is the only blanket UI-01 skip: a form that never changes
// anything. A GET form (no method means GET) is a search or filter, and a
// dialog form only closes its dialog. A form with an htmx verb is not skipped.
// Everything else that must skip UI-01 is a named allowlist entry.
func uiNoSubmitRule(formTag string) bool {
	if uiHTMXVerb.MatchString(formTag) {
		return false
	}
	m := uiMethodAttr.FindStringSubmatch(formTag)
	if m == nil {
		return !strings.Contains(formTag, "method=") // method="{{...}}" is not skipped
	}
	method := strings.ToLower(strings.TrimSpace(m[1]))
	return method == "get" || method == "dialog"
}

// UI-01: a submit button inside a form lies in a footer, and a primary or
// danger button is never followed by a secondary one in the same footer.
func lintUI01(f uiFile, add func(int, string, string)) {
	spans := footerSpans(f.src)
	var aside []uiSpan
	for _, m := range uiAsideOpen.FindAllStringIndex(f.src, -1) {
		if e := strings.Index(f.src[m[1]:], "</span>"); e >= 0 {
			aside = append(aside, uiSpan{m[0], m[1] + e})
		}
	}
	inAside := func(p int) bool {
		for _, s := range aside {
			if s.has(p) {
				return true
			}
		}
		return false
	}
	inFooter := func(p int) bool {
		for _, s := range spans {
			if s.has(p) {
				return true
			}
		}
		return false
	}
	for _, m := range uiFormOpen.FindAllStringIndex(f.src, -1) {
		tag := f.src[m[0]:m[1]]
		if uiNoSubmitRule(tag) {
			continue
		}
		end := strings.Index(f.src[m[1]:], "</form>")
		if end < 0 {
			end = len(f.src) - m[1]
		}
		body := f.src[m[1] : m[1]+end]
		submit := func(rel int) {
			if !inFooter(m[1] + rel) {
				add(m[1]+rel, "UI-01", "submit button outside a footer (use hm/card-foot, hm/foot, .hm-savebar or a dialog footer)")
			}
		}
		for _, b := range uiButtonTag.FindAllStringIndex(body, -1) {
			tag := body[b[0]:b[1]]
			if strings.Contains(tag, `type="button"`) || strings.Contains(tag, `type="reset"`) || strings.Contains(tag, "hm-visually-hidden") {
				continue
			}
			submit(b[0])
		}
		for _, b := range uiInputSubmit.FindAllStringIndex(body, -1) {
			submit(b[0])
		}
		for _, b := range uiButtonCall.FindAllStringIndex(body, -1) {
			if strings.Contains(body[b[0]:b[1]], `"Type" "submit"`) {
				submit(b[0])
			}
		}
	}
	for _, s := range spans {
		type btn struct {
			pos     int
			primary bool
		}
		var bs []btn
		region := f.src[s.start:s.end]
		for _, b := range uiButtonCall.FindAllStringIndex(region, -1) {
			bs = append(bs, btn{s.start + b[0], uiPrimaryCall.MatchString(region[b[0]:b[1]])})
		}
		for _, b := range uiButtonTag.FindAllStringIndex(region, -1) {
			bs = append(bs, btn{s.start + b[0], uiPrimaryRaw.MatchString(region[b[0]:b[1]])})
		}
		sort.Slice(bs, func(i, j int) bool { return bs[i].pos < bs[j].pos })
		seen := false
		for _, b := range bs {
			if inAside(b.pos) {
				continue // Delete sits apart at the far left
			}
			if seen && !b.primary {
				add(b.pos, "UI-01", "secondary action after a primary or danger button in the same footer")
			}
			seen = seen || b.primary
		}
	}
}

// UI-02: the savebar and card footer class strings live in their partials,
// their CSS and the sticky editor templates and the robots policy form only.
func lintUI02(f uiFile, add func(int, string, string)) {
	if f.name == "partials/card.html" || uiStickyTemplates[f.name] {
		return
	}
	for _, m := range uiSavebarCls.FindAllStringIndex(f.src, -1) {
		add(m[0], "UI-02", "hm-savebar and hm-card__foot belong to their partials; use hm/card-foot, hm/foot or hm/dialog-end")
	}
}

func lintUI02CSS(f uiFile, add func(int, string, string)) {
	if f.name == "css/partials/card.css" || f.name == "css/partials/savebar.css" {
		return
	}
	for _, m := range uiSavebarCls.FindAllStringIndex(f.src, -1) {
		add(m[0], "UI-02", "hm-savebar and hm-card__foot are styled in their own css files only")
	}
}

// UI-03: no raw checkbox and no checkbox control.
func lintUI03(f uiFile, add func(int, string, string)) {
	for _, m := range uiCheckboxTag.FindAllStringIndex(f.src, -1) {
		if strings.Contains(f.src[m[0]:m[1]], `role="switch"`) {
			continue
		}
		add(m[0], "UI-03", `raw type="checkbox": a lone on/off setting is an hm/field switch`)
	}
	for _, m := range uiCheckboxCtl.FindAllStringIndex(f.src, -1) {
		add(m[0], "UI-03", `hm/field Control "checkbox": a lone on/off setting is a switch`)
	}
}

// UI-05 (templates): no hand-written hm-btn, hm-card or hm-badge class outside the
// component partials.
func lintUI05HTML(f uiFile, add func(int, string, string)) {
	if uiComponentPartials[f.name] {
		return
	}
	for _, m := range uiClassAttr.FindAllStringIndex(f.src, -1) {
		attr := uiTemplateAct.ReplaceAllString(f.src[m[0]:m[1]], " ")
		attr = strings.TrimSpace(attr[strings.Index(attr, "=")+1:])
		for _, c := range strings.Fields(strings.Trim(attr, `"'`)) {
			if c == "hm-btn" || c == "hm-card" || c == "hm-badge" {
				add(m[0], "UI-05", "hand-written "+c+" markup: use hm/button, hm/card-start or hm/badge")
				break
			}
		}
	}
}

// UI-05 (js): no hm-btn, hm-card or hm-badge class token in a string, a template literal
// (including after ${...}) or a class list call. A leading dot is a selector,
// not markup, and is not flagged.
func lintUI05JS(f uiFile, add func(int, string, string)) {
	off := 0
	for _, line := range strings.SplitAfter(f.src, "\n") {
		for _, m := range uiJSToken.FindAllStringSubmatchIndex(line, -1) {
			add(off+m[0], "UI-05", "hm-"+line[m[4]:m[5]]+" class in a JS string")
		}
		off += len(line)
	}
}

// selectorAt returns the selector text of the rule whose block holds pos.
func selectorAt(src string, pos int) string {
	open := strings.LastIndex(src[:pos], "{")
	if open < 0 {
		return ""
	}
	start := strings.LastIndexAny(src[:open], "};") + 1
	return strings.TrimSpace(src[start:open])
}

// UI-04: box-shadow is none, a ring, an inset shadow, --shadow-2/3 on a popup.
func lintUI04(f uiFile, add func(int, string, string)) {
	for _, m := range uiBannedFx.FindAllStringIndex(f.src, -1) {
		add(m[0], "UI-04", "drop-shadow and text-shadow are banned")
	}
	for _, m := range uiShadowDecl.FindAllStringSubmatchIndex(f.src, -1) {
		val := strings.TrimSpace(f.src[m[2]:m[3]])
		sel := selectorAt(f.src, m[0])
		if strings.HasPrefix(val, "none") {
			continue
		}
		popup := uiPopupRe.MatchString(sel)
		ok := true
		for _, part := range splitTop(val) {
			part = strings.TrimSpace(part)
			switch {
			case strings.HasPrefix(part, "inset "), strings.HasPrefix(part, "0 0 0 "):
			case (strings.HasPrefix(part, "var(--shadow-2)") || strings.HasPrefix(part, "var(--shadow-3)")) && popup:
			default:
				ok = false
			}
		}
		if !ok {
			add(m[0], "UI-04", "box-shadow only on popups (--shadow-2/3), otherwise none, a ring or inset: "+val)
		}
	}
}

// uiPillParams are the query params an in-place pill row may change: they
// rearrange data already on the page (a period, a grouping, a view). A segmented
// row declares which of them its items change with "Changes"; Chrome (UI-D9)
// checks that the rendered links change nothing else. Every other row of tabs
// navigates and is an underline row.
var uiPillParams = map[string]bool{"group": true, "from": true, "to": true, "range": true, "view": true}

var (
	uiTabsCall     = regexp.MustCompile(`\{\{template "hm/tabs"[^}]*\}\}`)
	uiTabsVariant  = regexp.MustCompile(`"Variant" "([^"]*)"`)
	uiTabsChanges  = regexp.MustCompile(`"Changes" "([^"]*)"`)
	uiPageHdrCall  = regexp.MustCompile(`\{\{template "hm/pageheader"[^}]*\}\}`)
	uiPostForm     = regexp.MustCompile(`<form\b[^>]*method="post"[^>]*>`)
	uiTableCall    = regexp.MustCompile(`\{\{template "hm/(table|datatable)"|class="hm-(themes|pluginlist)"`)
	uiBorderCtl    = regexp.MustCompile(`border[\w-]*\s*:[^;}]*--border-control`)
	uiRestingField = regexp.MustCompile(`(?s)\.hm-field__control\s*\{([^}]*)\}`)
	uiHoverField   = regexp.MustCompile(`(?s)\.hm-field__control:hover\s*\{([^}]*)\}`)
	uiHoverColour  = regexp.MustCompile(`border(-top)?(-color)?\s*:[^;}]*var\(--(color-hai|border-control)\)`)
)

// UI-08: a row of tabs is an underline row (no Variant) or the level-2 "sub" row,
// because it navigates. A segmented row must say what its items change
// ("Changes" "view", from the in-place params) and Variant is always a literal,
// so the row cannot be a navigating one under a friendly label.
func lintUI08(f uiFile, add func(int, string, string)) {
	if f.name == "partials/tabs.html" {
		return
	}
	for _, m := range uiTabsCall.FindAllStringIndex(f.src, -1) {
		call := f.src[m[0]:m[1]]
		if strings.Contains(call, `"Variant" .`) || strings.Contains(call, `"Variant" (`) || strings.Contains(call, `"Variant" $`) {
			add(m[0], "UI-08", "hm/tabs Variant must be a literal so the lint can see what kind of row this is")
			continue
		}
		variant := ""
		if v := uiTabsVariant.FindStringSubmatch(call); v != nil {
			variant = v[1]
		}
		switch variant {
		case "", "sub", "tablist":
		case "segmented":
			c := uiTabsChanges.FindStringSubmatch(call)
			if c == nil {
				add(m[0], "UI-08", `a segmented row must declare what it changes: "Changes" "view" (one or more of group, from, to, range, view); a row that navigates is a tab row: drop Variant`)
				continue
			}
			for _, p := range strings.Fields(c[1]) {
				if !uiPillParams[p] {
					add(m[0], "UI-08", `"Changes" names "`+p+`", which is not an in-place param (group, from, to, range, view): a row that changes it navigates, use tabs`)
				}
			}
		default:
			add(m[0], "UI-08", `hm/tabs Variant is empty, "segmented", "sub" or "tablist", not "`+variant+`"`)
		}
	}
}

// uiCrumbEditors are the templates whose editor bar owns its own breadcrumb.
var uiCrumbEditors = map[string]bool{
	"layouts/editor.html": true, "pages/entities/blogedit.html": true,
}

// UI-09: the trail lives in the top bar only. The page header takes no Crumbs
// and no page renders a panel breadcrumb of its own.
func lintUI09(f uiFile, add func(int, string, string)) {
	if f.name == "partials/pageheader.html" {
		if i := strings.Index(f.src, "Crumbs"); i >= 0 {
			add(i, "UI-09", "the page header renders no breadcrumbs; handlers set View.Crumbs for the top bar")
		}
		return
	}
	if uiCrumbEditors[f.name] {
		return
	}
	for _, m := range uiPageHdrCall.FindAllStringIndex(f.src, -1) {
		if strings.Contains(f.src[m[0]:m[1]], `"Crumbs"`) {
			add(m[0], "UI-09", "do not pass Crumbs to hm/pageheader: set View.Crumbs in the handler")
		}
	}
	for _, m := range regexp.MustCompile(`class="hm-crumbs`).FindAllStringIndex(f.src, -1) {
		add(m[0], "UI-09", "a panel breadcrumb: the top bar already shows the trail")
	}
}

// dialogSpans finds the regions between hm/dialog-start and hm/dialog-end.
func dialogSpans(src string) []uiSpan {
	var out []uiSpan
	for from := 0; ; {
		i := strings.Index(src[from:], `{{template "hm/dialog-start"`)
		if i < 0 {
			return out
		}
		i += from
		j := strings.Index(src[i:], `{{template "hm/dialog-end"`)
		if j < 0 {
			return append(out, uiSpan{i, len(src)})
		}
		out = append(out, uiSpan{i, i + j})
		from = i + j
	}
}

// UI-10: a page that lists data (a table, or the theme and plugin lists) shows the list first. A create form is never a
// page-level form beside the table: it lives in a dialog (with a /new page for
// the same fields), and a reveal card is gone.
func lintUI10(f uiFile, add func(int, string, string)) {
	if !strings.HasPrefix(f.name, "pages/") {
		return
	}
	for _, m := range regexp.MustCompile(`hm-reveal`).FindAllStringIndex(f.src, -1) {
		add(m[0], "UI-10", "a reveal card hides a form on a list page: use a dialog from the header action")
	}
	if !uiTableCall.MatchString(f.src) {
		return
	}
	dialogs := dialogSpans(f.src)
	for _, m := range uiPostForm.FindAllStringIndex(f.src, -1) {
		inDialog := false
		for _, d := range dialogs {
			inDialog = inDialog || d.has(m[0])
		}
		if !inDialog {
			add(m[0], "UI-10", "a post form on a list page sits outside a dialog: create from a dialog opened by the header action")
		}
	}
}

// UI-11: inputs are calm at rest (--border-field), darker on hover (--color-hai or --border-control), and page CSS
// never puts the control border colour back on them.
func lintUI11(f uiFile, add func(int, string, string)) {
	if f.name == "css/partials/field.css" {
		m := uiRestingField.FindStringSubmatchIndex(f.src)
		switch {
		case m == nil:
			add(0, "UI-11", "field.css has no .hm-field__control rule")
		case !strings.Contains(f.src[m[2]:m[3]], "var(--border-field)"):
			add(m[0], "UI-11", ".hm-field__control must use var(--border-field) at rest")
		}
		h := uiHoverField.FindStringSubmatchIndex(f.src)
		switch {
		case h == nil:
			add(0, "UI-11", "field.css has no .hm-field__control:hover rule")
		case !uiHoverColour.MatchString(f.src[h[2]:h[3]]):
			add(h[0], "UI-11", ".hm-field__control:hover must darken the border to var(--color-hai) or var(--border-control)")
		}
		return
	}
	for _, m := range uiBorderCtl.FindAllStringIndex(f.src, -1) {
		if sel := selectorAt(f.src, m[0]); strings.Contains(sel, ".hm-field__control") || strings.Contains(sel, ".hm-input-affix") {
			add(m[0], "UI-11", "text inputs use var(--border-field) at rest, not --border-control")
		}
	}
}

// splitTop splits a shadow list on commas that are not inside parentheses.
func splitTop(s string) []string {
	var out []string
	depth, from := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[from:i])
				from = i + 1
			}
		}
	}
	return append(out, s[from:])
}

// uiComponents lists the whole component class tokens in one selector.
func uiComponents(sel string) []string {
	var out []string
	for _, c := range uiComponent.FindAllStringSubmatch(sel, -1) {
		if c[3] == "" {
			out = append(out, c[1])
		}
	}
	return out
}

// uiLoneSelector reports whether sel is exactly one component class, with
// only simple pseudos (:hover, ::before, :nth-child(2)) and no other class, id,
// attribute, combinator or functional pseudo such as :is() or :where().
func uiLoneSelector(sel string) bool {
	return uiLoneComp.MatchString(uiSimplePseudo.ReplaceAllString(strings.TrimSpace(sel), ""))
}

// UI-06: page and partial css must not restyle a shared component with a
// descendant or compound selector (.page .hm-btn, .hm-btn.page-x,
// .hm-card.is-x, .hm-btn[data-x], :is(.hm-btn, ...), :where(...)), unless the
// file is the component's own partial css.
func lintUI06(f uiFile, add func(int, string, string)) {
	if !strings.HasPrefix(f.name, "css/pages/") && !strings.HasPrefix(f.name, "css/partials/") {
		return
	}
	for _, m := range uiCSSRule.FindAllStringSubmatchIndex(f.src, -1) {
		sel := f.src[m[2]:m[3]]
		if strings.HasPrefix(strings.TrimSpace(sel), "@") {
			continue
		}
		if end := strings.Index(f.src[m[1]:], "}"); end < 0 || !uiRestyleProp.MatchString(f.src[m[1]:m[1]+end]) {
			continue // layout only (width, margin, flex, ...) places a component, it does not restyle it
		}
		for _, one := range splitTop(sel) {
			one = strings.TrimSpace(one)
			comps := uiComponents(one)
			if len(comps) == 0 || uiLoneSelector(one) {
				continue
			}
			own := true
			for _, c := range comps {
				if uiCSSOwner[c] != f.name {
					own = false
				}
			}
			if own {
				continue
			}
			add(m[2]+strings.Index(sel, one), "UI-06", "css restyles a shared component (."+comps[0]+") with a descendant or compound selector: "+one)
		}
	}
}

// UI-07: a form with more than one submit button must start with the primary
// save action or a hidden default submit (hm-visually-hidden), because Enter
// presses the first submit in source order. A secondary such as Preview must
// never be first. Buttons inside the form body are counted, plus buttons
// earlier in the file that join the form with form="id" (editor top bars);
// those are safe when primary or when they run the same save as the hidden one.
func lintUI07(f uiFile, add func(int, string, string)) {
	for _, m := range uiFormOpen.FindAllStringIndex(f.src, -1) {
		if uiNoSubmitRule(f.src[m[0]:m[1]]) {
			continue
		}
		end := strings.Index(f.src[m[1]:], "</form>")
		if end < 0 {
			end = len(f.src) - m[1]
		}
		body := f.src[m[1] : m[1]+end]
		type sub struct {
			pos  int
			safe bool // primary or hidden: safe to be first
		}
		var subs []sub
		for _, b := range uiButtonCall.FindAllStringIndex(body, -1) {
			if c := body[b[0]:b[1]]; strings.Contains(c, `"Type" "submit"`) && !strings.Contains(c, `(attrmap "form"`) {
				subs = append(subs, sub{b[0], strings.Contains(c, `"Variant" "primary"`)})
			}
		}
		for _, b := range uiButtonTag.FindAllStringIndex(body, -1) {
			tag := body[b[0]:b[1]]
			if strings.Contains(tag, `type="button"`) || strings.Contains(tag, `type="reset"`) || strings.Contains(tag, ` form="`) {
				continue // a button with form= belongs to another form
			}
			subs = append(subs, sub{b[0], strings.Contains(tag, "hm-visually-hidden") || strings.Contains(tag, "hm-btn--primary")})
		}
		for _, b := range uiInputSubmit.FindAllStringIndex(body, -1) {
			subs = append(subs, sub{b[0], false})
		}
		if id := uiFormID.FindStringSubmatch(f.src[m[0]:m[1]]); id != nil {
			before := f.src[:m[0]]
			for _, b := range uiButtonCall.FindAllStringIndex(before, -1) {
				if c := before[b[0]:b[1]]; strings.Contains(c, `"Type" "submit"`) && strings.Contains(c, `(attrmap "form" "`+id[1]+`"`) {
					subs = append(subs, sub{b[0] - m[1], strings.Contains(c, `"Variant" "primary"`) || strings.Contains(c, `"value" "save"`)})
				}
			}
			for _, b := range uiButtonTag.FindAllStringIndex(before, -1) {
				if tag := before[b[0]:b[1]]; strings.Contains(tag, `form="`+id[1]+`"`) && !strings.Contains(tag, `type="button"`) && !strings.Contains(tag, `type="reset"`) {
					subs = append(subs, sub{b[0] - m[1], strings.Contains(tag, "hm-btn--primary") || strings.Contains(tag, `value="save"`)})
				}
			}
		}
		if len(subs) < 2 {
			continue
		}
		sort.Slice(subs, func(i, j int) bool { return subs[i].pos < subs[j].pos })
		if !subs[0].safe {
			add(m[1]+subs[0].pos, "UI-07", "first submit of a multi-submit form is not the primary action: Enter would press it; add a hidden default submit first (hm-visually-hidden)")
		}
	}
}

// applyAllow drops allowlisted violations and reports entries that are empty
// of a reason or stale.
func applyAllow(allow []uiAllowEntry, vs []uiViolation) (left []uiViolation, bad []string) {
	lines := make([]map[int]bool, len(allow))
	for i := range lines {
		lines[i] = map[int]bool{}
	}
	for _, v := range vs {
		hit := false
		for i, a := range allow {
			if a.Rule == v.Rule && a.File == v.File && strings.Contains(v.Text, a.Needle) {
				lines[i][v.Line], hit = true, true
			}
		}
		if !hit {
			left = append(left, v)
		}
	}
	for i, a := range allow {
		switch {
		case strings.TrimSpace(a.Reason) == "":
			bad = append(bad, fmt.Sprintf("allowlist entry %s %s %q has no reason", a.Rule, a.File, a.Needle))
		case bareTrigger(a.Needle):
			bad = append(bad, fmt.Sprintf("allowlist entry %s %s %q is just the rule trigger: name the element (id, label, data hook)", a.Rule, a.File, a.Needle))
		case len(lines[i]) == 0:
			bad = append(bad, fmt.Sprintf("stale allowlist entry %s %s %q matches nothing: remove it", a.Rule, a.File, a.Needle))
		case len(lines[i]) > a.maxLines():
			bad = append(bad, fmt.Sprintf("allowlist entry %s %s %q matches %d lines (max %d): use a more specific needle", a.Rule, a.File, a.Needle, len(lines[i]), a.maxLines()))
		}
	}
	return left, bad
}

func TestUILint(t *testing.T) {
	t.Parallel()

	fsys := web.Admin()
	if files := readUIFiles(t, fsys); len(files) < 100 {
		t.Fatalf("found %d admin files: wrong root?", len(files))
	}
	left, bad := applyAllow(uiAllow, lintAdmin(t, fsys))
	for _, v := range left {
		t.Error(v.String())
	}
	for _, b := range bad {
		t.Error(b)
	}
	t.Logf("%d UI violations, %d allowlist entries", len(left), len(uiAllow))
}

// Each rule must fire on a planted violation, or the lint is not checking it.
func TestUILintSelfTest(t *testing.T) {
	t.Parallel()

	planted := fstest.MapFS{
		"pages/x/a.html": {Data: []byte(`<div class="hm-savebar"></div>` + "\n" + // UI-02
			`<form method="post" class="hm-stack"><button type="submit">Save</button></form>` + "\n" + // UI-01
			`<input type="checkbox" name="a">` + "\n" + // UI-03
			`<a class="hm-btn" href="/">Go</a>` + "\n")}, // UI-05
		"pages/x/b.html": {Data: []byte(`<form method="post">{{template "hm/card-foot"}}` +
			`{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit"}}` +
			`{{template "hm/button" dict "Label" "Cancel"}}{{template "hm/card-foot-end"}}</form>`)}, // UI-01 order
		"pages/x/d.html":  {Data: []byte(`<form method="post">{{template "hm/card-foot"}}<button type="submit">Preview</button><button type="submit" class="hm-btn--primary">Save</button>{{template "hm/card-foot-end"}}</form>`)}, // UI-07
		"pages/x/c.html":  {Data: []byte(`<input type="checkbox" role="switch" name="ok">`)},                                                                                                                                        // a switch: no violation
		"css/pages/x.css": {Data: []byte(".a { box-shadow: var(--shadow-1); }\n.hm-pe__x .hm-btn { border: 0; }\n")},                                                                                                                // UI-04, UI-06
		"js/x.js":         {Data: []byte("el('button', { class: 'hm-btn hm-btn--sm' });\n")},                                                                                                                                        // UI-05
	}
	got := map[string][]string{}
	for _, v := range lintAdmin(t, planted) {
		got[v.Rule] = append(got[v.Rule], v.String())
	}
	for _, rule := range []string{"UI-01", "UI-02", "UI-03", "UI-04", "UI-05", "UI-06", "UI-07"} {
		if len(got[rule]) == 0 {
			t.Errorf("%s did not fire on its planted violation", rule)
		}
	}
	if n := len(got["UI-01"]); n != 2 {
		t.Errorf("UI-01 fired %d times, want 2 (bare submit and secondary after primary): %v", n, got["UI-01"])
	}
	for _, s := range got["UI-03"] {
		if strings.HasPrefix(s, "pages/x/c.html") {
			t.Errorf("role=switch checkbox was flagged: %s", s)
		}
	}

	// The allowlist machinery itself.
	vs := lintAdmin(t, planted)
	left, bad := applyAllow([]uiAllowEntry{
		{Rule: "UI-02", File: "pages/x/a.html", Needle: `<div class="hm-savebar"></div>`, Reason: "planted"},
		{Rule: "UI-02", File: "pages/x/a.html", Needle: "never-there", Reason: "planted"},
		{Rule: "UI-05", File: "js/x.js", Needle: "hm-btn--sm", Reason: ""},
	}, vs)
	if len(bad) != 2 {
		t.Errorf("want one stale and one reasonless entry reported, got %v", bad)
	}
	for _, v := range left {
		if v.Rule == "UI-02" {
			t.Errorf("allowlisted UI-02 still reported: %s", v)
		}
	}
}

func TestUILintAllowlistHasReasons(t *testing.T) {
	t.Parallel()

	for _, a := range uiAllow {
		if strings.TrimSpace(a.Reason) == "" || a.Needle == "" {
			t.Errorf("allowlist entry %+v needs a needle and a reason", a)
		}
	}
}

func TestUILintRulesDocumented(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../../../docs/ADMIN_UI_RULES.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"UI-01", "UI-02", "UI-03", "UI-04", "UI-05", "UI-06", "UI-07", "UI-08", "UI-09", "UI-10", "UI-11"} {
		if !strings.Contains(string(b), rule) {
			t.Errorf("%s is missing from docs/ADMIN_UI_RULES.md", rule)
		}
	}
}

// lintOne lints a single planted file and returns "line RULE" strings.
func lintOne(t *testing.T, name, src string) []string {
	t.Helper()
	var out []string
	for _, v := range lintAdmin(t, fstest.MapFS{name: {Data: []byte(src)}}) {
		out = append(out, fmt.Sprintf("%d %s", v.Line, v.Rule))
	}
	return out
}

// Every detection added after review has its own planted case, positive and
// negative, so a regression in a regexp cannot pass silently.
func TestUILintSelfTestDetections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, file, src string
		want            []string // "line RULE", in order
	}{
		// UI-06: compound and functional selectors outside the owner's css.
		{"descendant", "css/pages/x.css", ".page .hm-btn { background: red; }", []string{"1 UI-06"}},
		{"compound after", "css/pages/x.css", ".hm-btn.page-x { background: red; }", []string{"1 UI-06"}},
		{"compound before", "css/pages/x.css", ".page-x.hm-btn { background: red; }", []string{"1 UI-06"}},
		{"card state", "css/pages/x.css", ".hm-card.is-x { padding: 0; }", []string{"1 UI-06"}},
		{"attribute", "css/pages/x.css", ".hm-btn[data-x] { color: red; }", []string{"1 UI-06"}},
		{"is()", "css/pages/x.css", ":is(.hm-btn, .y) { color: red; }", []string{"1 UI-06"}},
		{"where()", "css/pages/x.css", ":where(.hm-card) { padding: 0; }", []string{"1 UI-06"}},
		{"not() with a class", "css/pages/x.css", ".hm-btn:not(.y) { color: red; }", []string{"1 UI-06"}},
		{"list member", "css/pages/x.css", ".a, .b.hm-btn { color: red; }", []string{"1 UI-06"}},
		{"lone with pseudo", "css/pages/x.css", ".hm-btn:hover { color: red; }\n.hm-btn::after { color: red; }\n.hm-btn--ghost:not(:disabled) { color: red; }", nil},
		{"layout only", "css/pages/x.css", ".page .hm-btn { margin: 0; width: 10px; }", nil},
		{"prefix is not the component", "css/pages/x.css", ".hm-btn-count.x { color: red; }", nil},
		{"owner css", "css/partials/button.css", ".hm-btn.is-x { color: red; }\n.hm-btn[data-x] { color: red; }", nil},
		// UI-06: the families the consistency work made canonical each have one owner.
		{"segmented restyled", "css/pages/x.css", `.hm-foo .hm-segmented__btn[aria-pressed="true"] { background: var(--color-accent); }`, []string{"1 UI-06"}},
		{"tablist restyled", "css/pages/x.css", ".hm-tablist.hm-x { padding: 0 var(--space-4); }", []string{"1 UI-06"}},
		{"tab restyled", "css/pages/x.css", ".hm-foo .hm-tabs__tab { color: red; }", []string{"1 UI-06"}},
		{"table header restyled", "css/pages/x.css", ".hm-foo .hm-table thead th { text-transform: none; }", []string{"1 UI-06"}},
		{"badge restyled", "css/pages/x.css", ".hm-foo .hm-badge { color: red; }", []string{"1 UI-06"}},
		{"stat card restyled", "css/pages/x.css", ".hm-foo .hm-statcard__value { font-size: 12px; }", []string{"1 UI-06"}},
		{"stat grid restyled", "css/pages/x.css", ".hm-foo.hm-statgrid { background: red; }", []string{"1 UI-06"}},
		{"badge list is not the badge", "css/pages/x.css", ".hm-badgelist li { color: red; }", nil},
		{"segmented owner css", "css/partials/segmented.css", ".hm-foo .hm-segmented__btn { background: red; }", nil},
		{"table layout only", "css/pages/x.css", ".hm-foo .hm-table { margin: 0; }", nil},

		// UI-04: popup match is a whole class token.
		{"menutree is not a popup", "css/pages/x.css", ".hm-menutree__item { box-shadow: var(--shadow-2); }", []string{"1 UI-04"}},
		{"dialogue is not a popup", "css/pages/x.css", ".hm-dialogue { box-shadow: var(--shadow-3); }", []string{"1 UI-04"}},
		{"menu is a popup", "css/pages/x.css", ".hm-menu { box-shadow: var(--shadow-2); }\n.hm-menu__item { box-shadow: var(--shadow-2); }\n.hm-dialog--wide { box-shadow: var(--shadow-3); }", nil},

		// UI-04: --shadow-1 has no exception left (the selected segment is a paper pill drawn with a ring and a bevel, no lift).
		{"selected segment has no lift", "css/partials/tabs.css", `.hm-tabs[data-variant="segmented"] .hm-tabs__tab[aria-current="page"] { box-shadow: var(--shadow-1); }`, []string{"1 UI-04"}},
		{"shadow-1 on a card", "css/pages/x.css", ".hm-card { box-shadow: var(--shadow-1); }", []string{"1 UI-04"}},
		{"shadow-1 on a tab hover", "css/pages/x.css", ".hm-tabs__tab:hover { box-shadow: var(--shadow-1); }", []string{"1 UI-04"}},

		// UI-08: tabs navigate, pills rearrange in place.
		{"segmented navigation", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "Redirect tools" "Variant" "segmented" "Items" .Tabs}}`, []string{"1 UI-08"}},
		{"segmented named View", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "View" "Variant" "segmented" "Items" .Tabs}}`, []string{"1 UI-08"}},
		{"segmented changing a tab param", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "View" "Variant" "segmented" "Changes" "tab" "Items" .Tabs}}`, []string{"1 UI-08"}},
		{"variant from a variable", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "View" "Variant" .Variant "Items" .Tabs}}`, []string{"1 UI-08"}},
		{"variant from a call", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "View" "Variant" (or .V "sub") "Items" .Tabs}}`, []string{"1 UI-08"}},
		{"unknown variant", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "Redirect tools" "Variant" "pill" "Items" .Tabs}}`, []string{"1 UI-08"}},
		{"declared pill rows", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "View" "Variant" "segmented" "Changes" "view" "Items" .Tabs}}` + "\n" + `{{template "hm/tabs" dict "Label" "Group the chart" "Variant" "segmented" "Changes" "group" "Items" .G}}` + "\n" + `{{template "hm/tabs" dict "Label" "Date range" "Variant" "segmented" "Changes" "from to" "Items" .P}}`, nil},
		{"tab rows", "pages/x/a.html", `{{template "hm/tabs" dict "Label" "Redirect tools" "Items" .Tabs}}` + "\n" + `{{template "hm/tabs" dict "Label" "Crawler views" "Items" .Tabs "Variant" "sub"}}`, nil},

		// UI-09: no panel breadcrumbs.
		{"crumbs to the header", "pages/x/a.html", `{{template "hm/pageheader" dict "Title" "x" "Crumbs" .Crumbs}}`, []string{"1 UI-09"}},
		{"panel crumbs markup", "pages/x/a.html", `<nav class="hm-crumbs" aria-label="Breadcrumb"></nav>`, []string{"1 UI-09"}},
		{"header partial crumbs", "partials/pageheader.html", `{{if .Crumbs}}x{{end}}`, []string{"1 UI-09"}},
		{"header without crumbs", "pages/x/a.html", `{{template "hm/pageheader" dict "Title" "x"}}`, nil},

		// UI-10: list first, create in a dialog.
		{"form after the table", "pages/x/a.html", `{{template "hm/table" .Table}}` + "\n" + `<form method="post" action="/x"></form>`, []string{"2 UI-10"}},
		{"form before the table", "pages/x/a.html", `<form method="post" action="/x"></form>` + "\n" + `{{template "hm/table" .Table}}`, []string{"1 UI-10"}},
		{"form in a dialog", "pages/x/a.html", `{{template "hm/table" .Table}}` + "\n" + `{{template "hm/dialog-start" dict "ID" "n" "Title" "New"}}<form id="f" method="post" action="/x"></form>{{template "hm/dialog-end" dict "Primary" "Go"}}`, nil},
		{"form page without a list", "pages/x/a.html", `<form method="post" action="/x"></form>`, nil},
		{"form beside the theme list", "pages/x/a.html", `<div class="hm-themes"></div>` + "\n" + `<form method="post" action="/x"></form>`, []string{"2 UI-10"}},
		{"form beside the plugin list", "pages/x/a.html", `<ul class="hm-pluginlist" role="list"></ul>` + "\n" + `<form method="post" action="/x"></form>`, []string{"2 UI-10"}},
		{"reveal card", "pages/x/a.html", `<div class="hm-reveal" id="new"></div>`, []string{"1 UI-10"}},

		// UI-11: calm inputs at rest.
		{"control border on a field", "css/pages/x.css", ".hm-field__control { border: 1px solid var(--border-control); }", []string{"1 UI-11"}},
		{"control border on an affix", "css/pages/x.css", ".hm-input-affix { border-color: var(--border-control); }", []string{"1 UI-11"}},
		{"control border elsewhere", "css/pages/x.css", ".hm-x { border: 1px solid var(--border-control); }", nil},
		{"field.css at rest", "css/partials/field.css", ".hm-field__control { border: 1px solid var(--border-control); }\n.hm-field__control:hover { border-color: var(--color-hai); }", []string{"1 UI-11"}},
		{"field.css soft", "css/partials/field.css", ".hm-field__control { border: 1px solid var(--border-field); }\n.hm-field__control:hover { border-color: var(--color-hai); }", nil},
		{"field.css hover control", "css/partials/field.css", ".hm-field__control { border: 1px solid var(--border-field); }\n.hm-field__control:hover { border-color: var(--border-control); }", nil},
		{"field.css hover red", "css/partials/field.css", ".hm-field__control { border: 1px solid var(--border-field); }\n.hm-field__control:hover { border-color: red; }", []string{"2 UI-11"}},
		{"field.css hover as the resting colour", "css/partials/field.css", ".hm-field__control { border: 1px solid var(--border-field); }\n.hm-field__control:hover { border-color: var(--border-field); }", []string{"2 UI-11"}},
		{"field.css no hover", "css/partials/field.css", ".hm-field__control { border: 1px solid var(--border-field); }", []string{"1 UI-11"}},

		// UI-05: single quotes, unquoted, JS template literals.
		{"single quoted class", "pages/x/a.html", `<a class='hm-btn' href="/">Go</a>`, []string{"1 UI-05"}},
		{"single quoted list", "pages/x/a.html", `<div class='hm-row hm-card'></div>`, []string{"1 UI-05"}},
		{"unquoted class", "pages/x/a.html", `<a class=hm-btn href="/">Go</a>`, []string{"1 UI-05"}},
		{"template literal", "js/x.js", "el.innerHTML = `<button class=\"hm-btn\">Go</button>`;", []string{"1 UI-05"}},
		{"template literal expr", "js/x.js", "el.className = `${base} hm-btn`;", []string{"1 UI-05"}},
		{"two on a line", "js/x.js", "a.className = 'hm-btn'; b.className = 'hm-card';", []string{"1 UI-05", "1 UI-05"}},
		{"hand-written badge", "pages/x/a.html", `<span class="hm-badge hm-badge--info">New</span>`, []string{"1 UI-05"}},
		{"badge after other attributes", "pages/x/a.html", `<span data-hm-x hidden class="hm-badge hm-badge--warning">Unsaved</span>`, []string{"1 UI-05"}},
		{"badge in a class list", "pages/x/a.html", `<div class="row hm-badge"></div>`, []string{"1 UI-05"}},
		{"badge template call is fine", "pages/x/a.html", `{{template "hm/badge" (dict "Label" "New")}}`, nil},
		{"badge dot is not the badge", "pages/x/a.html", `<span class="hm-badge__dot"></span>`, nil},
		{"badge partial is exempt", "partials/badge.html", `<span class="hm-badge hm-badge--x"></span>`, nil},
		{"badge class in a JS string", "js/x.js", `el.className = "hm-badge hm-badge--info";`, []string{"1 UI-05"}},
		{"badge in a template literal", "js/x.js", "el.className = `${base} hm-badge`;", []string{"1 UI-05"}},
		{"badge selector is not markup", "js/x.js", `document.querySelector(".hm-badge");`, nil},
		{"badge dot element is not the badge", "js/x.js", `el.className = "hm-badge__dot";`, nil},
		{"selector is not markup", "js/x.js", "document.querySelector('.hm-btn');\nel.classList.add('hm-btn--sm');", nil},

		// UI-01: only GET and dialog forms skip; the rest are named allowlist entries.
		{"hm-inline post", "pages/x/a.html", `<form method="post" class="hm-inline"><button type="submit">Go</button></form>`, []string{"1 UI-01"}},
		{"role=search post", "pages/x/a.html", `<form method="post" role="search"><button type="submit">Go</button></form>`, []string{"1 UI-01"}},
		{"templated method", "pages/x/a.html", `<form method="{{.M}}"><button type="submit">Go</button></form>`, []string{"1 UI-01"}},
		{"htmx form", "pages/x/a.html", `<form hx-post="/x"><button type="submit">Go</button></form>`, []string{"1 UI-01"}},
		{"partial form", "partials/x.html", `<form method="post"><button type="submit">Go</button></form>`, []string{"1 UI-01"}},
		{"method=get", "pages/x/a.html", `<form method="get" class="hm-inline"><button type="submit">Go</button></form>`, nil},
		{"no method is get", "pages/x/a.html", `<form role="search"><button type="submit">Go</button></form>`, nil},
		{"method=dialog", "partials/x.html", `<form method="dialog"><button type="submit">Close</button></form>`, nil},

		// UI-07: Enter must press the primary action.
		{"preview first", "pages/x/a.html", `<form method="post">{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Preview" "Type" "submit"}}{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit"}}{{template "hm/card-foot-end"}}</form>`, []string{"1 UI-07"}},
		{"outside secondary first", "pages/x/a.html", `{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Preview" "Type" "submit" "Attrs" (attrmap "form" "ed")}}{{template "hm/card-foot-end"}}<form id="ed" method="post">{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit"}}{{template "hm/card-foot-end"}}</form>`, []string{"1 UI-07"}},
		{"outside primary first", "pages/x/a.html", `{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit" "Attrs" (attrmap "form" "ed")}}{{template "hm/card-foot-end"}}<form id="ed" method="post">{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit"}}{{template "hm/card-foot-end"}}</form>`, nil},
		{"hidden default first", "pages/x/a.html", `<form method="post"><button type="submit" class="hm-visually-hidden" tabindex="-1" aria-hidden="true">Save</button>{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Preview" "Type" "submit"}}{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit"}}{{template "hm/card-foot-end"}}</form>`, nil},
		{"primary first", "pages/x/a.html", `<form method="post">{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit"}}{{template "hm/button" dict "Label" "Preview" "Type" "submit"}}{{template "hm/card-foot-end"}}</form>`, []string{"1 UI-01"}},
		{"single submit", "pages/x/a.html", `<form method="post">{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Preview" "Type" "submit"}}{{template "hm/card-foot-end"}}</form>`, nil},
		{"form-owned buttons are not counted", "pages/x/a.html", `<form method="post">{{template "hm/card-foot"}}{{template "hm/button" dict "Label" "Clear" "Type" "submit" "Attrs" (attrmap "form" "other")}}{{template "hm/button" dict "Label" "Save" "Variant" "primary" "Type" "submit"}}{{template "hm/card-foot-end"}}</form>`, nil},
	}
	for _, c := range cases {
		got := lintOne(t, c.file, c.src)
		// UI-01 ordering noise: compare as multisets of "line RULE".
		sort.Strings(got)
		want := append([]string(nil), c.want...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The allowlist cannot exempt a whole file: a needle equal to the trigger, or one
// that covers more than a few lines, is reported.
func TestUILintAllowlistMeta(t *testing.T) {
	t.Parallel()

	src := ""
	for i := 0; i < 5; i++ {
		src += `<a class="hm-btn x` + fmt.Sprint(i) + `" href="/">Go</a>` + "\n"
	}
	vs := lintAdmin(t, fstest.MapFS{"pages/x/a.html": {Data: []byte(src)}})
	if len(vs) != 5 {
		t.Fatalf("planted 5 UI-05 violations, got %d", len(vs))
	}
	for _, needle := range []string{`class="hm-btn`, "hm-btn", `class='hm-btn'`, "<button", "hm-card", ""} {
		_, bad := applyAllow([]uiAllowEntry{{Rule: "UI-05", File: "pages/x/a.html", Needle: needle, Reason: "planted"}}, vs)
		if len(bad) == 0 || (needle != "" && !strings.Contains(bad[0], "just the rule trigger")) && needle != "" {
			t.Errorf("needle %q was accepted: %v", needle, bad)
		}
	}
	// Specific but too wide.
	_, bad := applyAllow([]uiAllowEntry{{Rule: "UI-05", File: "pages/x/a.html", Needle: `href="/"`, Reason: "planted"}}, vs)
	if len(bad) != 1 || !strings.Contains(bad[0], "matches 5 lines") {
		t.Errorf("a needle covering 5 lines was accepted: %v", bad)
	}
	// An explicit Max covers it, up to the hard cap.
	left, bad := applyAllow([]uiAllowEntry{{Rule: "UI-05", File: "pages/x/a.html", Needle: `href="/"`, Max: 5, Reason: "planted"}}, vs)
	if len(bad) != 0 || len(left) != 0 {
		t.Errorf("Max 5 should cover 5 lines: %v %v", bad, left)
	}
	_, bad = applyAllow([]uiAllowEntry{{Rule: "UI-05", File: "pages/x/a.html", Needle: `href="/"`, Max: 50, Reason: "planted"}}, vs)
	if len(bad) != 0 {
		t.Errorf("5 lines is under the hard cap of %d: %v", uiAllowHardMax, bad)
	}
	if (uiAllowEntry{Max: 50}).maxLines() != uiAllowHardMax {
		t.Errorf("Max must be capped at %d", uiAllowHardMax)
	}
	// Exact one-line needle works.
	left, bad = applyAllow([]uiAllowEntry{{Rule: "UI-05", File: "pages/x/a.html", Needle: `x3"`, Reason: "planted"}}, vs)
	if len(bad) != 0 || len(left) != 4 {
		t.Errorf("a one-line needle should drop exactly one violation: %v %d", bad, len(left))
	}
}

// The meta-checks cover the badge rule: a bare trigger needle is refused and a specific
// one drops only its own line.
func TestUILintAllowlistMetaBadge(t *testing.T) {
	t.Parallel()

	src := `<span class="hm-badge hm-badge--info">New</span>` + "\n" + `<span data-x class="hm-badge">Old</span>` + "\n"
	vs := lintAdmin(t, fstest.MapFS{"pages/x/a.html": {Data: []byte(src)}})
	if len(vs) != 2 {
		t.Fatalf("planted 2 hand-written badges, got %d: %v", len(vs), vs)
	}
	for _, needle := range []string{`class="hm-badge`, "hm-badge", ""} {
		if _, bad := applyAllow([]uiAllowEntry{{Rule: "UI-05", File: "pages/x/a.html", Needle: needle, Reason: "planted"}}, vs); len(bad) == 0 {
			t.Errorf("badge needle %q was accepted", needle)
		}
	}
	left, bad := applyAllow([]uiAllowEntry{{Rule: "UI-05", File: "pages/x/a.html", Needle: "Old", Reason: "planted"}}, vs)
	if len(bad) != 0 || len(left) != 1 {
		t.Errorf("a one-line needle should drop exactly one badge: %v %d", bad, len(left))
	}
}

// Every real entry already passes the meta-checks through TestUILint; this
// guards the table itself against a bare trigger needle sneaking in unused.
func TestUILintAllowlistNeedlesAreSpecific(t *testing.T) {
	t.Parallel()

	for _, a := range uiAllow {
		if bareTrigger(a.Needle) {
			t.Errorf("allowlist entry %s %s %q is a bare trigger", a.Rule, a.File, a.Needle)
		}
		if a.Max > uiAllowHardMax {
			t.Errorf("allowlist entry %s %s %q declares Max %d above %d", a.Rule, a.File, a.Needle, a.Max, uiAllowHardMax)
		}
	}
}
