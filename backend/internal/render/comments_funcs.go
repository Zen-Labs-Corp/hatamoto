package render

import (
	"bytes"
	"fmt"
	"io"
	"time"
)

// Ago is a coarse relative age. Unit is "now", "min", "hour", "day" or
// "date" (older than 30 days: print the date); N is the count for the first
// three.
type Ago struct {
	Unit string
	N    int
}

// ago buckets the age of t against nowFunc.
func ago(t time.Time) Ago {
	d := nowFunc().Sub(t)
	switch {
	case d < time.Minute:
		return Ago{Unit: "now"}
	case d < time.Hour:
		return Ago{"min", int(d / time.Minute)}
	case d < 24*time.Hour:
		return Ago{"hour", int(d / time.Hour)}
	case d <= 30*24*time.Hour:
		return Ago{"day", int(d / (24 * time.Hour))}
	}
	return Ago{Unit: "date"}
}

// RenderCommentsFragment writes the answer to an htmx comment post: the
// comments/form partial (which replaces #hm-comment-form) and, when item is set,
// the new comment as an out-of-band insert at the top of the list.
func (e *Engine) RenderCommentsFragment(w io.Writer, v *PageView, item *CommentItem) error {
	c := e.current()
	le, ok, err := c.pickEntry("single", "page")
	if err != nil || !ok {
		return fmt.Errorf("render comments fragment: no layout (%v)", err)
	}
	nv := *v
	nv.strs = resolveStrings(c.theme.Strings, nv.StringOverrides)
	d := &pageData{PageView: nv, Strings: nv.strs, Settings: *e.settings.Load()}
	var buf bytes.Buffer
	if err := safeExecute(&buf, le.tmpl, "comments/form", d); err != nil {
		return fmt.Errorf("render comments/form: %w", err)
	}
	if item != nil {
		buf.WriteString(`<div hx-swap-oob="afterbegin:#hm-comments-list">`)
		err := safeExecute(&buf, le.tmpl, "comments/item", map[string]any{"Item": *item, "Strings": d.Strings})
		if err != nil {
			return fmt.Errorf("render comments/item: %w", err)
		}
		buf.WriteString(`</div>`)
	}
	_, err = w.Write(buf.Bytes())
	return err
}
