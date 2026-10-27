package render

import (
	"html/template"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

// Rich text and html are sanitised on every render, not only at save time:
// a doc that reached storage another way never skips the policy.
func init() {
	registerWidget("richtext", func(w *widgetCtx) (any, error) {
		return template.HTML(entity.SanitizeRichText(str(w.P, "html"))), nil // #nosec G203 -- sanitised above
	})
	registerWidget("html", func(w *widgetCtx) (any, error) {
		w.Class = "hm-html hm-w-html"                                               // hm-w-html lets the lint skip the author's style attributes
		return template.HTML(entity.SanitizeHTMLSectionBody(str(w.P, "html"))), nil // #nosec G203 -- sanitised above
	})
	registerWidget("quote", func(w *widgetCtx) (any, error) {
		return struct{ Text, Cite, Role string }{str(w.P, "text"), str(w.P, "cite"), str(w.P, "role")}, nil
	})
	registerWidget("list", func(w *widgetCtx) (any, error) {
		type item struct {
			Text string
			Icon template.HTML
		}
		ic := str(w.P, "icon")
		if ic != "" {
			w.Class = "hm-list hm-list--icons"
		} else {
			w.Class = "hm-list"
		}
		var items []item
		for _, l := range textLines(str(w.P, "items"), maxListItems) {
			items = append(items, item{Text: l, Icon: iconInline(ic, "hm-list__i")})
		}
		return struct {
			Ordered bool
			Items   []item
		}{w.P["ordered"] == true, items}, nil
	})
	registerWidget("table", func(w *widgetCtx) (any, error) {
		rows := tsvRows(str(w.P, "rows"))
		x := struct {
			Caption string
			Head    []string
			Body    [][]string
		}{Caption: str(w.P, "caption")}
		if w.P["header"] == true && len(rows) > 0 {
			x.Head, rows = rows[0], rows[1:]
		}
		x.Body = rows
		return x, nil
	})
}

const (
	maxListItems = 200
	maxTableRows = 200
	maxTableCols = 20
)

// textLines splits on newlines, trims each line and drops empty ones.
func textLines(s string, limit int) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(out) == limit {
				break
			}
			out = append(out, l)
		}
	}
	return out
}

// tsvRows parses tab-separated text: blank lines are dropped, rows past
// maxTableRows and cells past maxTableCols are cut, short rows are padded to
// the widest one.
func tsvRows(s string) [][]string {
	var rows [][]string
	width := 0
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(rows) == maxTableRows {
			break
		}
		cells := strings.Split(l, "\t") // not trimmed first: a leading tab is an empty first cell
		if len(cells) > maxTableCols {
			cells = cells[:maxTableCols]
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		width = max(width, len(cells))
		rows = append(rows, cells)
	}
	for i, r := range rows {
		for len(r) < width {
			r = append(r, "")
		}
		rows[i] = r
	}
	return rows
}
