package render

import (
	"fmt"
	"regexp"
	"strings"
)

// RefusalKind says, in one word, why a plugin template is not registered.
type RefusalKind string

const (
	RefusalRegistration    RefusalKind = "registration"
	RefusalVet             RefusalKind = "vet"
	RefusalEntry           RefusalKind = "entry"
	RefusalCallGraph       RefusalKind = "call-graph"
	RefusalRangeDepth      RefusalKind = "range-depth"
	RefusalScripts         RefusalKind = "scripts"
	RefusalMarkup          RefusalKind = "markup"
	RefusalOutputCap       RefusalKind = "output-cap"
	RefusalMissingTemplate RefusalKind = "missing-template"
	RefusalParse           RefusalKind = "parse"
	RefusalOther           RefusalKind = "other"
)

// TemplateRefusal is one refused-template entry explained for the admin.
type TemplateRefusal struct {
	Kind     RefusalKind
	Template string // "section promo", "partial card", "all templates"; "" when the text names none
	Reason   string // one plain sentence
	Raw      string // the entry as listed (API, logs)
}

// ExplainRefusals explains each entry of a plugin's refused-template list. It
// reads the raw text the producers make, so it is for the admin read path, not
// for rendering. It returns nil when raw is empty.
func ExplainRefusals(raw []string) []TemplateRefusal {
	if len(raw) == 0 {
		return nil
	}
	out := make([]TemplateRefusal, len(raw))
	for i, r := range raw {
		out[i] = ExplainRefusal(r)
	}
	return out
}

// ExplainRefusal explains one refused-template entry in a plain sentence.
// Text that matches no known producer gets the RefusalOther sentence.
func ExplainRefusal(raw string) TemplateRefusal {
	kind := refusalKind(raw)
	reason := refusalSentences[kind]
	if kind == RefusalMissingTemplate {
		reason = fmt.Sprintf(`Uses {{template "%s"}} which this plugin does not define.`, missingTemplateName(raw))
	}
	return TemplateRefusal{Kind: kind, Template: refusalLabel(raw), Reason: reason, Raw: raw}
}

var refusalSentences = map[RefusalKind]string{
	RefusalRegistration: "The plugin registered it with a name, type or source that is not allowed, or with a name already taken.",
	RefusalVet:          "It defines or calls a template that does not belong to this plugin.",
	RefusalEntry:        "It does not define its own section template.",
	RefusalCallGraph:    fmt.Sprintf("Its {{template}} calls loop back on themselves or add up to more than %d calls.", maxPluginCalls),
	RefusalRangeDepth:   fmt.Sprintf("It nests {{range}} loops over the content more than %d deep, counting the templates it calls.", maxPluginRangeDepth),
	RefusalScripts:      "It contains a script, and the plugin does not hold site.scripts.",
	RefusalMarkup:       "It puts a template action where the markup rules do not allow one, such as inside a tag, a comment or a URL scheme.",
	RefusalOutputCap:    fmt.Sprintf("Its output went over the %d KiB limit in the test render.", maxPackOutput>>10),
	RefusalParse:        "It has a template syntax error.",
	RefusalOther:        "It was left out when the plugin's templates were compiled.",
}

var (
	noSuchTemplateRe = regexp.MustCompile(`no such template "([^"]+)"`)
	labelEntryRe     = regexp.MustCompile(`^(section|partial) ([^\s:]+): `)
	labelPlainPlugin = regexp.MustCompile(`^plugin (section|partial) [^/\s]+/([^\s:]+):`)
	labelDupPartial  = regexp.MustCompile(`^plugin partial plugin/[^/\s]+/(\S+) already registered`)
	labelDupSection  = regexp.MustCompile(`^section type "([^"]+)" already registered`)
	pluginPathPrefix = regexp.MustCompile(`^plugin/[^/]+/`)
)

func missingTemplateName(raw string) string {
	if m := noSuchTemplateRe.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return ""
}

// refusalKind classifies a raw entry; the first match wins.
func refusalKind(raw string) RefusalKind {
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(raw, s) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.Contains(raw, errPluginOutput.Error()):
		return RefusalOutputCap
	case has("directly or through other templates", "expands to more than"):
		return RefusalCallGraph
	case has("nests ranges deeper than"):
		return RefusalRangeDepth
	case has("action-context", "a branch ends inside", "belong in text content", "inside a tag name, an attribute name"):
		return RefusalMarkup
	case strings.HasSuffix(raw, "(not allowed without site.scripts)"), has("markup: banned-", "sample render"):
		return RefusalScripts
	case strings.HasPrefix(raw, "invalid plugin/name"), strings.HasPrefix(raw, "plugin partial "),
		strings.HasPrefix(raw, "plugin section "), strings.HasPrefix(raw, "plugin func "), strings.HasPrefix(raw, "section type "):
		return RefusalRegistration
	case has(`must define "section-`, "does not define its section template"):
		return RefusalEntry
	case noSuchTemplateRe.MatchString(raw):
		return RefusalMissingTemplate
	case has("which is not a name of its own", "may not call another plugin's templates", "partial name outside"):
		return RefusalVet
	case has("template: ", "html/template"):
		return RefusalParse
	}
	return RefusalOther
}

// refusalLabel names the template an entry is about ("section promo",
// "partial card", "all templates"); "" when the text names none.
func refusalLabel(raw string) string {
	short := func(kind, name string) string { return kind + " " + pluginPathPrefix.ReplaceAllString(name, "") }
	if strings.HasPrefix(raw, "all templates") {
		return "all templates"
	}
	if m := labelEntryRe.FindStringSubmatch(raw); m != nil {
		return short(m[1], m[2])
	}
	if m := labelDupPartial.FindStringSubmatch(raw); m != nil {
		return "partial " + m[1]
	}
	if m := labelPlainPlugin.FindStringSubmatch(raw); m != nil {
		return m[1] + " " + m[2]
	}
	if m := labelDupSection.FindStringSubmatch(raw); m != nil {
		return "section " + m[1]
	}
	return ""
}
