package wpimport

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

// LineStatus is how well one thing of the source was carried over.
type LineStatus string

// The statuses of a report line.
const (
	LineMapped   LineStatus = "mapped"
	LinePartly   LineStatus = "partly"
	LineImported LineStatus = "imported"
	LineSkipped  LineStatus = "skipped"
	LineNote     LineStatus = "note"
)

// ReportLine is one line of the custom fields and forms report. It holds
// titles, slugs and counts only, never values of the source site.
type ReportLine struct {
	Status LineStatus
	Label  string
	Detail string
}

// ReportSection is a titled group of report lines.
type ReportSection struct {
	Title   string
	Summary string
	Lines   []ReportLine
}

// reportTitleWidth is the width of the title column of the text report.
const reportTitleWidth = 44

// acfStructured reports whether a field type gets a line of its own.
func acfStructured(typ string) bool {
	switch typ {
	case "repeater", "group", "flexible_content", "relationship", "post_object", "page_link", "gallery":
		return true
	}
	return false
}

func acfTypeName(typ string) string { return strings.ReplaceAll(typ, "_", " ") }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ReportSections returns the ACF and Contact Form 7 sections of a result; nil
// when the result has neither, as for runs stored before they were imported.
func ReportSections(r *Result) []ReportSection {
	if r == nil || (r.ACF == nil && r.Forms == nil) {
		return nil
	}
	var out []ReportSection
	if r.ACF != nil {
		out = append(out, acfSection(r.ACF))
	}
	if r.Forms != nil {
		out = append(out, formsSection(r.Forms))
	}
	return out
}

func acfSection(a *ACFResult) ReportSection {
	sec := ReportSection{Title: "Custom fields (ACF)", Summary: plural(a.Groups, "group", "groups") + " found"}
	if a.TypesGuessed {
		sec.Summary += ", types guessed"
	}
	type key struct{ group, target string }
	var order []key
	simple := map[key][]string{}
	var rest []ACFFieldResult
	for _, f := range a.Fields {
		label := firstNonEmpty(f.Label, "field")
		if f.Status == "mapped" && !acfStructured(f.Type) && f.Type != "location" {
			k := key{f.Group, f.Target}
			if _, ok := simple[k]; !ok {
				order = append(order, k)
			}
			simple[k] = append(simple[k], fmt.Sprintf("%s (%s)", label, acfTypeName(f.Type)))
			continue
		}
		rest = append(rest, f)
	}
	for _, k := range order {
		sec.Lines = append(sec.Lines, ReportLine{Status: LineMapped, Label: k.group + " > " + strings.Join(simple[k], "  ")})
	}
	for _, f := range rest {
		sec.Lines = append(sec.Lines, acfFieldLine(f))
	}
	return sec
}

// acfFieldLine is the line of one field that is not a plain value.
func acfFieldLine(f ACFFieldResult) ReportLine {
	label := fmt.Sprintf("%s (%s)", firstNonEmpty(f.Label, "field"), acfTypeName(f.Type))
	switch {
	case f.Type == "location":
		return ReportLine{Status: LineSkipped, Label: f.Group, Detail: f.Note}
	case f.Status == "skipped":
		return ReportLine{Status: LineSkipped, Label: f.Group + " > " + label, Detail: f.Note}
	case f.Status == "partly" && strings.Contains(f.Note, "stored as text"):
		return ReportLine{Status: LinePartly, Label: label, Detail: "nested content kept as text, rebuild in the page editor"}
	case f.Status == "partly":
		return ReportLine{Status: LinePartly, Label: label, Detail: f.Note}
	}
	l := ReportLine{Status: LineMapped, Label: label, Detail: f.Note}
	switch f.Type {
	case "repeater":
		if f.Rows > 0 {
			l.Detail = plural(f.Rows, "row", "rows") + " rebuilt"
		}
	case "group":
		if f.Items > 0 {
			l.Detail = plural(f.Items, "item", "items") + " rebuilt"
		}
	case "flexible_content":
		if f.Items > 0 || f.Rows > 0 {
			l.Detail = fmt.Sprintf("%s, rows rebuilt with a \"layout\" key", plural(f.Items, "page", "pages"))
		}
	case "relationship", "post_object", "page_link":
		if f.Links > 0 {
			l.Detail = plural(f.Links, "link", "links") + ", second pass done"
		}
	case "gallery":
		l.Status, l.Detail = LinePartly, "kept as an image list; captions not carried"
	}
	return l
}

func formsSection(f *FormsResult) ReportSection {
	sec := ReportSection{Title: "Forms (Contact Form 7)", Summary: plural(f.Found, "form", "forms")}
	for _, x := range f.Forms {
		switch x.Status {
		case "already":
			sec.Lines = append(sec.Lines, ReportLine{Status: LineImported, Label: x.Title, Detail: "already imported"})
		case "skipped":
			sec.Lines = append(sec.Lines, ReportLine{Status: LineSkipped, Label: x.Title, Detail: x.Note})
		case "partly":
			detail := fmt.Sprintf("%s -> form %q", plural(x.Fields, "field", "fields"), x.Slug)
			if len(x.Dropped) > 0 {
				detail += "; " + strings.Join(x.Dropped, ", ")
			}
			sec.Lines = append(sec.Lines, ReportLine{Status: LinePartly, Label: x.Title, Detail: detail})
		default:
			detail := fmt.Sprintf("%s -> form %q; [contact-form-7] replaced by [form slug=%s]", plural(x.Fields, "field", "fields"), x.Slug, x.Slug)
			sec.Lines = append(sec.Lines, ReportLine{Status: LineImported, Label: x.Title, Detail: detail})
		}
	}
	if f.MailNotImported {
		sec.Lines = append(sec.Lines, ReportLine{Status: LineNote, Detail: "Notifications and mail templates are not imported: set the recipient under Forms."})
	}
	return sec
}

// Headline is the one-line outcome of a run for the report: what is new and
// what changed, so a re-run that did nothing says so.
func Headline(r *Result) string {
	n := r.Created
	changed := 0
	if r.ACF != nil {
		n += r.ACF.FieldsAdded
		changed = r.ACF.RelationsChanged
	}
	if r.Forms != nil {
		n += r.Forms.Created
	}
	prefix := "This run"
	if r.RunNumber > 0 {
		prefix = fmt.Sprintf("Run %d", r.RunNumber)
	}
	if n == 0 && changed == 0 {
		return prefix + ": nothing new, 0 changed"
	}
	return fmt.Sprintf("%s: %d new, %d changed", prefix, n, changed)
}

// WriteReportText writes the custom fields and forms report as plain text;
// nothing when the result has neither.
func WriteReportText(w io.Writer, r *Result) {
	secs := ReportSections(r)
	if secs == nil {
		return
	}
	fmt.Fprintf(w, "  %-*s%s\n", reportTitleWidth-2, "WordPress import report", Headline(r))
	for _, s := range secs {
		fmt.Fprintf(w, "  %-*s%s\n", reportTitleWidth-2, s.Title, s.Summary)
		for _, l := range s.Lines {
			fmt.Fprintln(w, reportLineText(l))
		}
	}
}

func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// reportLineText renders one line with its status glyph and word.
func reportLineText(l ReportLine) string {
	// Titles come from the source site: no terminal escapes.
	l.Label, l.Detail = stripControls(l.Label), stripControls(l.Detail)
	text := l.Label
	if l.Label != "" && l.Detail != "" {
		text += ": " + l.Detail
	} else if l.Detail != "" {
		text = l.Detail
	}
	var glyph, word string
	switch l.Status {
	case LineMapped:
		glyph, word = "✔", "Mapped"
	case LineImported:
		glyph, word = "✔", "Imported"
	case LinePartly:
		glyph, word = "~", "Partly"
	case LineSkipped:
		glyph, word = "✗", "Skipped"
	default:
		return "  " + text
	}
	return fmt.Sprintf("    %s %-12s%s", glyph, word, text)
}
