package permtext

import (
	"slices"
	"strings"
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
