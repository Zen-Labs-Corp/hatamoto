package designcheck

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"hatamoto.dev/core/web"
)

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
	{Rule: "UI-01", File: "partials/table.html", Needle: `data-hm-seobulk-save`, Reason: "row action of an editable list: the row's own form saves that one row without JavaScript"},
	{Rule: "UI-01", File: "pages/settings/theme.html", Needle: `(printf "Update to %s" .Update) "Variant" "secondary" "Size" "sm" "Type" "submit"`, Reason: "the Live card's Update action sits in its own one-button form in the card body, now that theme cards have no footer band"},
	{Rule: "UI-01", File: "partials/pluginhead.html", Needle: `"Label" "Review update"`, Reason: "the callout's action, a one-button form that opens the update prompt; it posts because the update route is a POST"},
	{Rule: "UI-01", File: "pages/settings/performance.html", Needle: `"Preload now"`, Reason: "setting row action bound to its own form by the form attribute"},
	{Rule: "UI-01", File: "pages/settings/layoutparts.html", Needle: `printf "Create %s part"`, Reason: "one create button per slot, each in its own one-button form in a row"},
	{Rule: "UI-01", File: "pages/entities/new.html", Needle: `<button class="hm-choice__link" type="submit">`, Reason: "a type card is its own one-button form: one click creates the draft, as the Create menu does"},
	{Rule: "UI-01", File: "pages/settings/security-change.html", Needle: `"Regenerate"`, Reason: "inline action beside the copy button: formaction posts a new candidate without changing the address"},
	{Rule: "UI-01", File: "pages/settings/updates.html", Needle: `"Check now"`, Reason: "setting row action bound to its own form by the form attribute"},
	{Rule: "UI-01", File: "pages/settings/updates.html", Needle: `"Unpin"`, Reason: "setting row action bound to its own form by the form attribute"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `"Turn off now"`, Reason: "banner action in its own form: turns the opt-in off at once"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `"Turn off"`, Reason: "banner action in its own form: turns the developer packages opt-in off at once"},
	{Rule: "UI-01", File: "pages/settings/theme.html", Needle: `"Turn off"`, Reason: "banner action in its own form: turns the developer packages opt-in off at once"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `Update all (`, Reason: "list action above the plugin list, a form of its own that updates every plugin without a review step"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `"Update to v%s"`, Reason: "list row action in a plugin row"},
	{Rule: "UI-01", File: "pages/settings/plugins.html", Needle: `"Review update to v%s"`, Reason: "list row action in a plugin row"},
	{Rule: "UI-01", File: "pages/settings/tracking.html", Needle: `"Turn off") "Turn on") "Size" "sm" "Type" "submit"`, Reason: "one-click on/off of a tag card, a one-button form in the card's own footer like a plugin card's Install"},
	{Rule: "UI-01", File: "pages/plugins/browse.html", Needle: `"Try again"`, Reason: "retry action of the unreachable-registry state, a form of its own"},
	{Rule: "UI-01", File: "pages/theme/browse-detail.html", Needle: `data-hm-panel-actions`, Reason: "drawer actions: pluginpanel.js moves this block into the dialog footer; the one-button POST form is the primary action"},
	{Rule: "UI-01", File: "pages/theme/browse.html", Needle: `"Try again"`, Reason: "retry action of the unreachable-registry state, a form of its own"},
	{Rule: "UI-01", File: "pages/entities/compare.html", Needle: `.RestoreLabel`, Reason: "list row action: restores one node of the layout, a form of its own in the changes table"},
	{Rule: "UI-01", File: "pages/media/picker.html", Needle: `"Upload"`, Reason: "inline upload beside its file input in a dialog"},
	{Rule: "UI-01", File: "pages/schemas/edit.html", Needle: `"Add field"`, Reason: "inline add row inside the editor, next to its select"},
	{Rule: "UI-01", File: "pages/schemas/edit.html", Needle: `value="up:{{$f.Key}}"`, Reason: "field row operations (move, remove) inside the editor"},
	{Rule: "UI-01", File: "pages/schemas/edit.html", Needle: `save_json`, Reason: "action of the advanced JSON disclosure, a nested form of its own"},
	{Rule: "UI-01", File: "pages/menus/edit.html", Needle: `"Add item"`, Reason: "inline add row next to its select"},
	{Rule: "UI-01", File: "pages/setup/admin-address.html", Needle: `"Label" "Regenerate"`, Reason: "inline action beside the address it regenerates, in the copy row"},
	{Rule: "UI-01", File: "pages/setup/done.html", Needle: `"Send me a test email"`, Reason: "one inline row: the email note and its test button, not a form footer"},
	{Rule: "UI-01", File: "layouts/shell.html", Needle: `"Label" "Got it"`, Reason: "acknowledgement in the owner's usage stats notice: one inline row of text and buttons, not a form footer"},
	{Rule: "UI-01", File: "partials/segmented.html", Needle: `class="hm-segmented__btn"`, Reason: "segmented control: each button posts its own value, the control is the footer-less form"},
	{Rule: "UI-01", File: "pages/linking/suggestions.html", Needle: `suggestions/insert`, Reason: "one-button POST form in a table row (was exempt by hm-inline)"},
	{Rule: "UI-01", File: "partials/builderoutline.html", Needle: `"Add to the end"`, Reason: "inline add row in the builder outline, next to its select"},
	{Rule: "UI-01", File: "partials/pageheader.html", Needle: `{{range .Items}}{{if .Post}}<form`, Reason: "menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "partials/pageheader.html", Needle: `{{with .Primary}}`, Reason: "one-button POST form in the page header"},
	{Rule: "UI-01", File: "partials/pluginhead.html", Needle: `{{if .Enabled}}disable`, Reason: "one-button POST form (Turn on or off) in the plugin header row"},
	{Rule: "UI-01", File: "partials/sectionhead.html", Needle: `{{with $.Primary}}`, Reason: "one-button POST form in the section header"},
	{Rule: "UI-01", File: "partials/pageheader.html", Needle: `id="hm-overflow"`, Reason: "overflow menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "partials/sectionhead.html", Needle: `id="hm-overflow"`, Reason: "overflow menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "pages/entities/builder.html", Needle: `<button role="menuitem">Duplicate</button>`, Reason: "overflow menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "pages/entities/blogedit.html", Needle: `<button role="menuitem">Duplicate</button>`, Reason: "overflow menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "partials/rowmenu.html", Needle: `{{$a.Post}}`, Reason: "row menu item that posts, inside a popup menu"},
	{Rule: "UI-01", File: "pages/settings/locales.html", Needle: `"remove_entry_path"`, Reason: "Remove of an entry path row in the Visitor entry panel: a row operation that re-renders the panel without saving, like the schema field row buttons"},
	{Rule: "UI-01", File: "pages/settings/locales.html", Needle: `"add_entry_path"`, Reason: "Add path of the Visitor entry panel: a row operation that re-renders the panel with one more input, without saving"},
	{Rule: "UI-01", File: "partials/sidebar.html", Needle: `action="{{adminPath}}/theme"`, Reason: "theme toggle in the sidebar"},
	{Rule: "UI-01", File: "partials/sidebar.html", Needle: `action="{{adminPath}}/logout"`, Reason: "sign out button in the sidebar"},
	{Rule: "UI-01", File: "partials/table.html", Needle: `data-hm-bulk-toolbar`, Reason: "bulk action toolbar above a table, not a form footer"},
	{Rule: "UI-01", File: "partials/formssubdrawer.html", Needle: `define "hm/formssubstatus"`, Reason: "submission drawer action: a one-button POST form in the drawer body (the dialog footer is the shared Close row), returning to the list"},
	{Rule: "UI-01", File: "partials/formssubdrawer.html", Needle: `>Archive</button>`, Reason: "submission drawer Archive: a one-button POST form in the More actions menu in the drawer body (the dialog footer is the shared Close row), returning to the list"},
	{Rule: "UI-01", File: "partials/formssubdrawer.html", Needle: `"Label" "Forward"`, Reason: "submission drawer Forward by email: the submit of the form in the More actions menu (the dialog footer is the shared Close row), returning to the list"},
	{Rule: "UI-01", File: "partials/mediatile.html", Needle: `data-hm-bulk-toolbar`, Reason: "bulk action toolbar above a media grid, not a form footer"},

	// UI-10: page-level forms that edit or remove something, not a create form beside the list.
	{Rule: "UI-10", File: "pages/linking/rules.html", Needle: `rules/delete-source`, Reason: "removes a set of rules by source, a destructive action and not a create form"},
	{Rule: "UI-10", File: "pages/settings/locales.html", Needle: `id="visitor-entry-form"`, Reason: "the Visitor entry settings form below the locales table, a settings form and not a create form; its Add path button re-renders the panel without saving"},
	{Rule: "UI-10", File: "pages/settings/theme-strings.html", Needle: `id="theme-strings"`, Reason: "the one settings form of the page: the table is its inputs (one text per row), saved together, and not a create form beside a list"},
	{Rule: "UI-10", File: "pages/settings/privacy.html", Needle: `action="{{adminPath}}/settings/privacy"`, Reason: "the cookie consent settings form, a settings form and not a create form; the counts table below is read-only"},
	{Rule: "UI-10", File: "pages/settings/privacy.html", Needle: `id="email-protection-form"`, Reason: "the Email addresses switch, a settings form and not a create form; the counts table below is read-only"},
	{Rule: "UI-10", File: "pages/settings/theme.html", Needle: `action="{{adminPath}}/plugins/developer/off"`, Reason: "Turn off in the developer packages banner, a one-click setting change and not a create form beside a list; it takes no input"},
	{Rule: "UI-10", File: "pages/settings/theme.html", Needle: `Update to %s`, Reason: "per-card Update button of an installed theme, a row action and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `action="{{adminPath}}/settings/updates" class`, Reason: "the update policy form, a settings form and not a create form; the history table below is read-only"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="check-form"`, Reason: "Check now, a row action of the status card and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="nobackup-form"`, Reason: "Update without a backup, a row action of the status card that opens a confirm page and is not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="apply-form"`, Reason: "Update now, a row action of the status card and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="retry-form"`, Reason: "Retry, a row action of the status card that asks for the password first and is not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="unpin-form"`, Reason: "Unpin, a row action of the policy card and not a create form"},
	{Rule: "UI-10", File: "pages/settings/updates.html", Needle: `id="stats-form"`, Reason: "the Usage stats checkbox, a settings form and not a create form"},
	{Rule: "UI-10", File: "pages/plugins/changeset.html", Needle: `data-hm-review-form`, Reason: "review page: decision forms (risky items, suggestions, publish) about the listed items, not a create form beside a list"},
	{Rule: "UI-10", File: "pages/plugins/runs.html", Needle: `id="run-form"`, Reason: "the Start form of a run: host ticks and inputs are core's, a settings-like form and not a create form beside the list; the recent runs table below is read-only"},
	{Rule: "UI-10", File: "pages/plugins/automatic.html", Needle: `name="run" value="{{.Rule.Run}}"`, Reason: "the allow form of a standing approval: a consent action about the rule on the page, not a create form beside a list; the daily summary table below is read-only"},
	{Rule: "UI-10", File: "pages/plugins/automatic.html", Needle: `action="{{.Off}}"`, Reason: "Turn off, a row action of the approved rule and not a create form beside a list; it takes no input"},
	{Rule: "UI-10", File: "pages/profile/security.html", Needle: `profile/security/totp/remove`, Reason: "Remove, a row action of the authenticator card and not a create form; the passkey table below lists existing keys"},
	{Rule: "UI-10", File: "pages/profile/security.html", Needle: `profile/security/totp/begin`, Reason: "Set up, a row action of the authenticator card that takes no input and not a create form"},
	{Rule: "UI-10", File: "pages/profile/security.html", Needle: `profile/security/codes">`, Reason: "Get new codes, a row action of the recovery codes card and not a create form"},
	{Rule: "UI-10", File: "pages/media/list.html", Needle: `id="image-formats-form"`, Reason: "the Image formats settings card above the library, a settings form and not a create form; the files table is a separate card"},
	{Rule: "UI-10", File: "pages/media/list.html", Needle: `id="rebuild-form"`, Reason: "Rebuild copies, a card action that starts a background job and takes no input, so there is nothing to put in a dialog"},
	{Rule: "UI-10", File: "pages/settings/backups.html", Needle: `id="offsite-form"`, Reason: "the Off-site copy settings card beside the backups list, a settings form and not a create form"},
	{Rule: "UI-10", File: "pages/settings/backups.html", Needle: `id="offsite-run-form"`, Reason: "Back up and upload now, a card action that starts a background job and takes no input"},
	{Rule: "UI-10", File: "pages/settings/backups.html", Needle: `id="backup-form"`, Reason: "Back up now, a header action that starts a background job and takes no input, so there is nothing to put in a dialog"},

	{Rule: "UI-10", File: "pages/settings/hosting.html", Needle: `id="hosting-form"`, Reason: "the hosting settings form, a settings form and not a create form; the islands and export log tables are read-only"},
	{Rule: "UI-10", File: "pages/settings/hosting.html", Needle: `id="test-form"`, Reason: "Test export, a card action that starts a background export of the saved target and takes no input"},
	{Rule: "UI-10", File: "pages/settings/hosting.html", Needle: `id="export-form"`, Reason: "Export now, a card action that starts a background export and takes no input"},
	{Rule: "UI-10", File: "pages/settings/hosting.html", Needle: `id="rebuild-form"`, Reason: "Rebuild all, a card action that asks for the password and starts a full export; it takes no input"},

	{Rule: "UI-10", File: "pages/entities/share.html", Needle: `action="{{.Action}}"`, Reason: "the lifetime picker of the share page: its result (a link shown once) has to appear on the page, and the table below lists the active links"},

	// UI-02: selectors that name the class, not markup that uses it.
	{Rule: "UI-02", File: "css/base.css", Needle: "html:has(.hm-savebar)", Reason: "reserves scroll space when a sticky footer exists"},

	// UI-03: checkboxes that are not a lone on/off setting.
	{Rule: "UI-03", File: "partials/filterbar.html", Needle: `"Control" "checkbox"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "partials/field.html", Needle: `hm-field__chipbox" type="checkbox"`, Reason: "toggle chip: one pill per option in a multi-pick group"},
	{Rule: "UI-03", File: "pages/settings/packageconfirm.html", Needle: `"confirm"`, Reason: reasonConsent},
	{Rule: "UI-03", File: "pages/settings/pluginprompt.html", Needle: `"confirm"`, Reason: reasonConsent},
	{Rule: "UI-03", File: "pages/plugins/permissions.html", Needle: `name="permission"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "partials/form-apikey.html", Needle: `"scopes"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/import/options.html", Needle: `.Types`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/settings/search-replace.html", Needle: `name="sel"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/entities/edit.html", Needle: `name="term"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "pages/entities/blogedit.html", Needle: `name="term"`, Reason: reasonMulti},
	{Rule: "UI-03", File: "partials/table.html", Needle: `data-hm-select-all`, Reason: "table select-all box"},
	{Rule: "UI-03", File: "partials/table.html", Needle: `data-hm-row-select`, Reason: "table row select box"},
	{Rule: "UI-03", File: "partials/mediatile.html", Needle: `name="id"`, Reason: "media tile select box"},
	{Rule: "UI-03", File: "schemaform/field.html", Needle: `__clear`, Reason: "clear the stored value, a destructive tick and not a setting"},

	// UI-04: shadows outside the popup list.
	{Rule: "UI-04", File: "css/partials/chart.css", Needle: ".hm-statcard { box-shadow", Reason: "stat cards carry the lightest shadow; the one named exception to the no-shadow card rule"},
	{Rule: "UI-04", File: "css/pages/blogeditor.css", Needle: ".hm-ed__bar", Reason: "sticky blog editor toolbar; editor_polish_test.go asserts blogeditor.css"},
	{Rule: "UI-04", File: "css/pages/builder.css", Needle: ".hm-bs__seo {", Reason: "the SEO drawer sits over the inspector like a popup"},

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
	{Rule: "UI-05", File: "js/canvas/host.js", Needle: `"Dismiss the tip"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/htmlblocks.js", Needle: `"Edit HTML"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/kit.js", Needle: `text: "Apply"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/kit.js", Needle: `text: "Cancel"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/editor/kit.js", Needle: `text: "Remove"`, Reason: reasonEditor},
	{Rule: "UI-05", File: "js/content.js", Needle: "data-hm-picker-close", Reason: "media picker dialog buttons built in JS"},
	{Rule: "UI-05", File: "pages/settings/theme.html", Needle: `id="theme-{{.Name}}"`, Reason: "theme card with a dynamic id, heading in the body row and an Active modifier"},
	{Rule: "UI-05", File: "pages/redirects/list.html", Needle: "hm-redirect-test", Reason: "inline test form styled as a card"},
	{Rule: "UI-05", File: "pages/entities/versions.html", Needle: "hm-compare", Reason: "inline compare form styled as a card"},
	{Rule: "UI-05", File: "pages/entities/compare.html", Needle: "hm-tablecard", Reason: "table card, no head or body"},
	{Rule: "UI-05", File: "pages/entities/version.html", Needle: "hm-tablecard", Reason: "table card, no head or body"},
	{Rule: "UI-05", File: "pages/media/list.html", Needle: "hm-card hm-media", Reason: "one flush card for the toolbar, storage row and both views; hm/card-start pads its body"},
	{Rule: "UI-05", File: "pages/entities/new.html", Needle: "hm-choice", Reason: "choice tile that is a list item"},
	{Rule: "UI-05", File: "pages/media/detail.html", Needle: "hm-media-detail__preview", Reason: "image preview tile, no head or body"},
	{Rule: "UI-05", File: "pages/schemas/edit.html", Needle: `id="advanced"`, Reason: "details disclosure styled as a card"},
	{Rule: "UI-05", File: "pages/linking/rules.html", Needle: `id="clean-up"`, Reason: "details disclosure styled as a card"},

	{Rule: "UI-04", File: "css/pages/pageeditor.css", Needle: ".hm-pe__fmt {", Reason: "the canvas formatting bar floats over the page like a popup"},
	{Rule: "UI-04", File: "css/pages/pageeditor.css", Needle: ".hm-pe__toolbar {", Reason: "the canvas element toolbar floats over the page like a popup"},
	// UI-06: the list toolbar sizes the controls it holds; the sizing lives in each control's owner file.
	{Rule: "UI-06", File: "css/partials/field.css", Needle: ".hm-listbar", Max: 3, Reason: "the list toolbar row sets its search affix to one 32px height and the md radius; listbar_css_test.go asserts it"},
	{Rule: "UI-06", File: "css/partials/button.css", Needle: ".hm-listbar__end", Reason: "the Filters button in the list toolbar takes the md radius like its neighbours; listbar_css_test.go asserts it"},
	{Rule: "UI-06", File: "css/partials/select.css", Needle: ".hm-listbar", Reason: "the list toolbar row sets the select trigger to 32px and the md radius; listbar_css_test.go asserts it"},
	{Rule: "UI-06", File: "css/partials/segmented.css", Needle: ".hm-listbar", Max: 3, Reason: "the list toolbar row sets the status segment track to 32px; listbar_css_test.go asserts it"},
	{Rule: "UI-06", File: "css/partials/tabs.css", Needle: ".hm-listbar", Max: 3, Reason: "the list toolbar row sets a segmented tab track to 32px; listbar_css_test.go asserts it"},
	// UI-06: editor and chip scope.
	{Rule: "UI-06", File: "css/partials/danger-confirm.css", Needle: ".hm-danger-confirm__card .hm-card__title", Reason: "danger title colour like .hm-dialog--danger; hm/card-start has no title class hook"},
	{Rule: "UI-06", File: "css/partials/table.css", Needle: ".hm-card__body > .hm-tablecard", Reason: "a list table nested in a titled card is its content, not a second card"},
	{Rule: "UI-06", File: "css/pages/blogeditor.css", Needle: ".hm-be__side .hm-card", Max: 4, Reason: "blog editor side panel; editor_polish_test.go asserts blogeditor.css"},
	{Rule: "UI-06", File: "css/pages/blogeditor.css", Needle: ".hm-field:has(> .hm-ed--blog)", Reason: "blog editor label chrome; editor_polish_test.go asserts blogeditor.css"},
	{Rule: "UI-06", File: "css/pages/content.css", Needle: ".hm-entities .hm-table th", Max: 2, Reason: "the entities list packs its rows tighter than the default table (padding only, on wide screens)"},
	{Rule: "UI-06", File: "css/pages/content.css", Needle: `input[type="file"].hm-field__control`, Reason: "native file input needs its own box"},
	{Rule: "UI-06", File: "css/partials/settings.css", Needle: `.hm-fieldgrid--tokens .hm-field__control:not([type="color"])`, Reason: "design token inputs are compact so the page is not four screens tall; colour inputs keep their own box"},
	{Rule: "UI-06", File: "css/partials/settings.css", Needle: ".hm-fieldgrid--steps > .hm-field", Max: 2, Reason: "design system scales are one row per step: label left, mono input right"},
	{Rule: "UI-06", File: "css/partials/settings.css", Needle: ".hm-fieldgrid--steps .hm-field__control", Reason: "right-aligned mono input of a scale row"},
	{Rule: "UI-06", File: "css/partials/settings.css", Needle: ".hm-card--preset .hm-card__head > p", Reason: "preset key and count read as one mono line; hm/card-start takes a plain string for the description"},
	{Rule: "UI-06", File: "css/partials/settings.css", Needle: ".hm-swatchrow .hm-field__label", Reason: "a swatch label sits in the tile grid above its mono hex; it keeps the UI font"},
	{Rule: "UI-06", File: "css/partials/steps.css", Needle: `.hm-field__control[type="color"]`, Max: 5, Reason: "native colour input needs its own box"},
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

func readUIFilesT(t *testing.T, fsys fs.FS) []uiFile {
	t.Helper()
	files, err := readUIFiles(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// lintAdmin runs UI-01 to UI-11 over every html, css and js file in fsys.
func lintAdmin(t *testing.T, fsys fs.FS) []uiViolation {
	t.Helper()
	vs, err := lintFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestUILint(t *testing.T) {
	t.Parallel()

	fsys := web.Admin()
	if files := readUIFilesT(t, fsys); len(files) < 100 {
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
