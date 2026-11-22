package llmstxt

import (
	"strings"
	"unicode"
)

// BuildIndex renders /llms.txt in the llmstxt.org shape: H1, blockquote
// summary, details paragraph, one H2 list per section, then "## Optional".
// Sections without items are omitted.
func BuildIndex(site Site, sections []Section) []byte {
	var b strings.Builder
	base := strings.TrimRight(site.BaseURL, "/")
	b.WriteString("# " + OneLine(site.Name, 0) + "\n")
	if s := OneLine(site.Summary, 0); s != "" {
		b.WriteString("\n> " + s + "\n")
	}
	if d := strings.TrimSpace(site.Details); d != "" {
		b.WriteString("\n" + d + "\n")
	}
	for _, sec := range sections {
		if len(sec.Items) == 0 {
			continue
		}
		b.WriteString("\n## " + OneLine(sec.Title, 0) + "\n")
		for _, it := range sec.Items {
			desc := it.Desc
			if sec.Kind == KindPeople {
				desc = it.Job
			}
			b.WriteString(itemLine(it.Name, base+it.Path, desc))
		}
	}
	b.WriteString("\n## Optional\n")
	b.WriteString(itemLine("Knowledge graph", base+"/api/v1/public/knowledge-graph", "JSON-LD"))
	b.WriteString(itemLine("Sitemap", base+"/sitemap.xml", ""))
	if site.HasFeed {
		b.WriteString(itemLine("Posts feed", base+"/feed.xml", ""))
	}
	if site.FullOn {
		b.WriteString(itemLine("Full text", base+"/llms-full.txt", "older posts and every page"))
	}
	return []byte(b.String())
}

func itemLine(name, url, desc string) string {
	name = strings.NewReplacer("[", `\[`, "]", `\]`).Replace(OneLine(name, 0))
	line := "- [" + name + "](" + url + ")"
	if desc = OneLine(desc, 0); desc != "" {
		line += ": " + desc
	}
	return line + "\n"
}

// OneLine collapses all whitespace runs to single spaces. When max > 0 the
// result is cut to max runes with a trailing "…".
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); max > 0 && len(r) > max {
		return strings.TrimRightFunc(string(r[:max-1]), unicode.IsSpace) + "…"
	}
	return s
}

// FirstSentence returns the first sentence of plain text: up to and including
// the first ". ", "! " or "? " (or the end), collapsed to one line.
func FirstSentence(plain string) string {
	s := OneLine(plain, 0)
	for i, r := range s {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(s) || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

// Details joins the non-empty location, contact and hours sentences of the
// business profile, one per line.
func Details(addr, contact, hours string) string {
	var parts []string
	for _, p := range []string{addr, contact, hours} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n")
}
