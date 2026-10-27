package admin

import (
	"encoding/json"
	"html/template"
)

// speculationExcluded lists the admin paths the browser must never prerender:
// auth and setup screens, session and theme actions, assets, previews, plugin
// pages (they embed a sandboxed frame), delete confirmations, submission
// details (opening one marks it read),
// entity editors (they boot and post previews), CSV exports and every POST target (a prerender
// is a GET issued without a click, so it must have no effect). Patterns are
// URLPattern pathnames below the admin prefix; "*" also matches across slashes.
var speculationExcluded = []string{
	"/login*", "/logout*", "/forgot*", "/reset*", "/accept*", "/setup*",
	"/theme", "/_/*",
	"/preview/*", "/*/preview*",
	"/plugins/*/ui*", "/plugins/*/settings*", "/plugins/*/bridge*",
	"/*/delete*", "/*/confirm*", "/settings/api-keys/secret*",
	"/*/export.csv*",
	"/*/revoke*", "/*/regenerate*", "/*/resend*", "/*/activate*", "/*/status*",
	"/*/toggle*", "/*/ignore*", "/*/restore*", "/*/email*", "/*/bulk*",
	"/*/start*", "/*/resume*", "/*/cancel*", "/*/fetch*", "/*/upload*",
	"/*/install*", "/*/unsigned*", "/*/enable*", "/*/disable*", "/*/grant*",
	"/settings/submissions/*", "/entities/*/edit*", "/entities/new*", "/entities/drafts*",
	"/*/insert*", "/*/uninstall*", "/*/autosave*", "/*/analyze*",
}

// speculationRulesFor is the JSON of the shell's inline speculationrules script:
// same-origin admin links under prefix are prerendered on hover (moderate
// eagerness).
func speculationRulesFor(prefix string) template.HTML {
	excluded := make([]string, len(speculationExcluded))
	for i, p := range speculationExcluded {
		excluded[i] = prefix + p
	}
	b, err := json.Marshal(map[string]any{"prerender": []any{map[string]any{
		"where": map[string]any{"and": []any{
			map[string]any{"href_matches": prefix + "/*"},
			map[string]any{"not": map[string]any{"href_matches": excluded}},
		}},
		"eagerness": "moderate",
	}}})
	if err != nil {
		return "{}"
	}
	return template.HTML(b)
}

// SpeculationRules is the template accessor for speculationRules.
func (v *View) SpeculationRules() template.HTML {
	if v.speculation != "" {
		return v.speculation
	}
	return speculationRulesFor(v.prefix())
}
