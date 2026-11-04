package permtext

import (
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

// Line is one permission of a directory card in plain English. Field names,
// types and order must match render.PermLine so a Line converts with
// render.PermLine(l).
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
