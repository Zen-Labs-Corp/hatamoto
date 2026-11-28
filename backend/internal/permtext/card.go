package permtext

import (
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

// Line is one permission of a plugin card in plain English (admin install
// prompt, CLI registry page).
type Line struct {
	Name, Text string
	Warn       bool
}

// Card is the directory view of a permission list.
type Card struct {
	Count int
	Warn  []string
	Lines []Line
}

// ForCard maps raw permission names to the directory view: the count, the
// distinct Short labels of risky permissions in order, and one line per
// permission. The warning icon already signals the risk, so the "WARNING: "
// prefix of the admin wording is dropped from the line text.
func ForCard(perms []string) Card {
	var c Card
	for _, p := range perms {
		t := Lookup(p)
		if t.Warn && !slices.Contains(c.Warn, t.Short) {
			c.Warn = append(c.Warn, t.Short)
		}
		c.Lines = append(c.Lines, Line{Name: p, Text: strings.TrimPrefix(t.Help, "WARNING: "), Warn: t.Warn})
	}
	c.Count = len(perms)
	return c
}

// CSPLine is one directive of a plugin's csp block in plain English.
type CSPLine struct {
	// Name is the label ("Scripts", "Frames", "Connections", "Images").
	Name  string
	Hosts []string
}

// CSPPrompt is the sentence a permission prompt puts before the CSPLines.
const CSPPrompt = "Add these sites to your public pages' security policy"

// CSPLines lists the non-empty lists of c, in a fixed order.
func CSPLines(c *sdk.CSP) []CSPLine {
	if c == nil {
		return nil
	}
	var out []CSPLine
	for _, l := range []CSPLine{
		{"Scripts", c.ScriptSrc}, {"Frames", c.FrameSrc}, {"Connections", c.ConnectSrc}, {"Images", c.ImgSrc},
	} {
		if len(l.Hosts) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// ContentTypesLine is the prompt sentence for the entity types a plugin
// installs, "Create the content types Product and Order".
func ContentTypesLine(names []string) string {
	if len(names) == 0 {
		return ""
	}
	label := "type"
	if len(names) > 1 {
		label = "types"
	}
	switch len(names) {
	case 1:
		return "Create the content " + label + " " + names[0]
	case 2:
		return "Create the content " + label + " " + names[0] + " and " + names[1]
	}
	return "Create the content " + label + " " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// TagLines is the prompt text for one tag of a tag pack: what core loads, from
// which hosts, after which consent, and which id the owner enters. A tag pack
// has no permissions: it adds a script from the hosts it declares.
func TagLines(idLabel string, hosts []string, category string, websockets bool) []Line {
	from := "It adds a script from " + strings.Join(hosts, ", ") + " to your public pages, loaded after the visitor agrees"
	if websockets {
		from += ", and may open live connections (WebSocket) to them"
	}
	lines := []Line{
		{Name: "Script", Text: from + "."},
		{Name: "Consent", Text: "Consent category: " + upperFirst(category) + " (loads only after the visitor agrees)."},
	}
	if idLabel != "" {
		lines = append(lines, Line{Name: "Needs", Text: "Needs: a " + idLabel + " (you enter it after installing)."})
	}
	return lines
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
