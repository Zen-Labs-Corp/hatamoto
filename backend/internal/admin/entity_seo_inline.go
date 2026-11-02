package admin

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The inline SEO view edits four values of an entry. Each is written through
// the writers the editors use (seoForm.merge, mergeFocusKeyword); no other
// attribute is read or changed.
const (
	seoFieldTitle       = "title"
	seoFieldDescription = "description"
	seoFieldKeyword     = "keyword"
	seoFieldNoIndex     = "noindex"
)

// seoFields are the values the inline view shows and edits.
type seoFields struct {
	Title, Description, Keyword string
	NoIndex                     bool
}

// seoEdit is one posted row: the new values, the values the row was loaded
// with, and which fields the row carried at all.
type seoEdit struct {
	New, Orig seoFields
	Has       map[string]bool
}

// seoPatchResult reports what applySEOEdit did. Attrs is the attribute map with
// the changes applied; Conflicts maps a field name to the stored value (as
// text) that stopped it from being written.
type seoPatchResult struct {
	Changed   []string
	Conflicts map[string]string
	Current   seoFields
	Attrs     map[string]any
}

// seoLengthError names the field whose value is too long.
type seoLengthError struct {
	Field string
	Max   int
}

func (e *seoLengthError) Error() string {
	return fmt.Sprintf("use at most %d characters for the SEO %s", e.Max, e.Field)
}

// seoFieldsOf reads the inline values from stored attributes.
func seoFieldsOf(attrs map[string]any) seoFields {
	seoMap, _ := attrs["seo"].(map[string]any)
	f := seoFrom(attrs)
	return seoFields{
		Title:       strings.TrimSpace(f.Title),
		Description: strings.TrimSpace(f.Description),
		Keyword:     strings.TrimSpace(str(seoMap, "focus_keyword")),
		NoIndex:     f.NoIndex,
	}
}

func (f seoFields) text(field string) string {
	switch field {
	case seoFieldTitle:
		return f.Title
	case seoFieldDescription:
		return f.Description
	case seoFieldKeyword:
		return f.Keyword
	}
	if f.NoIndex {
		return "true"
	}
	return ""
}

// applySEOEdit applies the changed fields of ed onto attrs (modified in place).
// A field counts as changed when it was posted and differs from its loaded
// value. If the stored value no longer equals the loaded one the field is a
// conflict and is left alone; the other fields still apply. The keyword is
// only written when allowKeyword is set.
func applySEOEdit(attrs map[string]any, ed seoEdit, allowKeyword bool) (seoPatchResult, error) {
	cur := seoFieldsOf(attrs)
	res := seoPatchResult{Conflicts: map[string]string{}, Current: cur, Attrs: attrs}
	next := cur
	for _, field := range []string{seoFieldTitle, seoFieldDescription, seoFieldKeyword, seoFieldNoIndex} {
		if !ed.Has[field] || (field == seoFieldKeyword && !allowKeyword) {
			continue
		}
		if ed.New.text(field) == ed.Orig.text(field) {
			continue
		}
		if cur.text(field) != ed.Orig.text(field) {
			res.Conflicts[field] = cur.text(field)
			continue
		}
		switch field {
		case seoFieldTitle:
			if utf8.RuneCountInString(ed.New.Title) > builderSEOTitleMax {
				return res, &seoLengthError{Field: "title", Max: builderSEOTitleMax}
			}
			next.Title = strings.TrimSpace(ed.New.Title)
		case seoFieldDescription:
			if utf8.RuneCountInString(ed.New.Description) > builderSEODescMax {
				return res, &seoLengthError{Field: "description", Max: builderSEODescMax}
			}
			next.Description = strings.TrimSpace(ed.New.Description)
		case seoFieldKeyword:
			next.Keyword = strings.TrimSpace(ed.New.Keyword)
		case seoFieldNoIndex:
			next.NoIndex = ed.New.NoIndex
		}
		res.Changed = append(res.Changed, field)
	}
	if len(res.Changed) == 0 {
		return res, nil
	}
	// Seed from the stored form so keys this view does not own keep their values.
	form := seoFrom(attrs)
	form.Title, form.Description, form.NoIndex = next.Title, next.Description, next.NoIndex
	had, hadNoIndex := attrs["no_index"]
	form.merge(attrs)
	if next.NoIndex == cur.NoIndex { // noindex untouched: keep how it was stored
		if hadNoIndex {
			attrs["no_index"] = had
		} else {
			delete(attrs, "no_index")
		}
	}
	if next.Keyword != cur.Keyword {
		mergeFocusKeyword(attrs, next.Keyword)
	}
	res.Current = seoFieldsOf(attrs)
	return res, nil
}
