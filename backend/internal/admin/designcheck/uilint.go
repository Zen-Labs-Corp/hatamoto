package designcheck

import (
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

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
	"pages/forms/settings.html":  true,
	"pages/taxonomies/term.html": true,
	"pages/plugins/plugin.html":  true,
	// The changeset review page: the publish bar stays in reach beside a long list of risky changes.
	"pages/plugins/changeset.html": true,
	// The menu editor: Save, Discard and the unsaved state stay in reach beside a long tree.
	"pages/menus/edit.html": true,
	// Titles & meta: one save bar below a long list of patterns.
	"pages/settings/seo-titles.html": true,
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

// readUIFiles returns every html, css and js file under fsys.
func readUIFiles(fsys fs.FS) ([]uiFile, error) {
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
	return out, err
}

// lintFS runs UI-01 to UI-11 over every html, css and js file in fsys.
func lintFS(fsys fs.FS) ([]uiViolation, error) {
	files, err := readUIFiles(fsys)
	if err != nil {
		return nil, err
	}
	var vs []uiViolation
	for _, f := range files {
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
	return vs, nil
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

// UI-09: the top bar is the only place a trail renders (besides the editor bar). The page header takes no Crumbs
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
