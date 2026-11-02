package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/entity"
)

// seoSaveTries is how often the inline save re-reads and re-applies after another
// writer bumped the version between the read and the write.
const seoSaveTries = 3

// seoCount is the character counter shown under a title or description cell.
type seoCount struct {
	Len    int    `json:"len"`
	Status string `json:"status"`
	Msg    string `json:"msg,omitempty"`
}

// seoInlineReply is the JSON answer of an inline SEO save. Status is "saved",
// "conflict" (a field changed elsewhere since the row loaded) or "error".
type seoInlineReply struct {
	Status    string              `json:"status"`
	Message   string              `json:"message,omitempty"`
	Values    seoFields           `json:"values"`
	Conflicts map[string]string   `json:"conflicts,omitempty"`
	Counters  map[string]seoCount `json:"counters"`
}

// seoCounter measures a typed title or description against the limits the SEO
// panel uses. An empty value is not a problem here: the template fills it in.
func seoCounter(field, text string) seoCount {
	n := utf8.RuneCountInString(strings.TrimSpace(text))
	c := seoCount{Len: n, Status: checkPass}
	lo, mid, hi := 30, 50, 60
	noun := "Title"
	if field == seoFieldDescription {
		lo, mid, hi, noun = 60, 120, 160, "Description"
	}
	switch {
	case n == 0:
	case n < lo:
		c.Status, c.Msg = checkFail, fmt.Sprintf("%s is too short.", noun)
	case n < mid:
		c.Status, c.Msg = checkWarn, fmt.Sprintf("%s could be longer.", noun)
	case n > hi:
		c.Status, c.Msg = checkWarn, fmt.Sprintf("%s may be cut off in results.", noun)
	}
	return c
}

func seoCounters(f seoFields) map[string]seoCount {
	return map[string]seoCount{seoFieldTitle: seoCounter(seoFieldTitle, f.Title), seoFieldDescription: seoCounter(seoFieldDescription, f.Description)}
}

// readSEOEdit decodes a posted row: the values and the loaded copies (orig_*).
// A field the row did not carry (no orig_ input) is never written.
func readSEOEdit(r *http.Request) seoEdit {
	_ = r.ParseForm()
	ed := seoEdit{Has: map[string]bool{}}
	for _, f := range []string{seoFieldTitle, seoFieldDescription, seoFieldKeyword, seoFieldNoIndex} {
		_, ed.Has[f] = r.PostForm["orig_"+f]
	}
	val := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	ed.New = seoFields{Title: val("title"), Description: val("description"), Keyword: val("keyword"), NoIndex: r.PostFormValue("noindex") == "true"}
	ed.Orig = seoFields{Title: val("orig_title"), Description: val("orig_description"), Keyword: val("orig_keyword"), NoIndex: r.PostFormValue("orig_noindex") == "true"}
	return ed
}

// wantsJSON is the fetch of the list script; without it the row form posts
// normally and the answer is a redirect with a flash.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// handleEntitySEOInline is POST /admin/entities/{id}/seo: the inline SEO view of
// the content list. It writes only the SEO values the row changed, through the
// same writers as the entity forms, and never touches status, name, slug or the
// rest of the attributes.
func (a *Admin) handleEntitySEOInline(w http.ResponseWriter, r *http.Request) {
	e, sc, ok := a.loadEntity(w, r)
	if !ok {
		return
	}
	c := a.deps.Content
	ed := readSEOEdit(r)
	back := backTo(r, a.adminPath+"/entities", a.adminPath+"/entities?view=seo")
	reply := func(status int, v seoInlineReply) {
		if wantsJSON(r) {
			a.writeJSON(w, status, v)
			return
		}
		kind := "success"
		if v.Status != "saved" {
			kind = "error"
		}
		a.SetFlash(w, r, kind, v.Message)
		Redirect(w, r, back)
	}
	fail := func(status int, cur seoFields, msg string) {
		reply(status, seoInlineReply{Status: "error", Message: msg, Values: cur, Counters: seoCounters(cur)})
	}
	for try := 0; try < seoSaveTries; try++ {
		if try > 0 {
			var err error
			if e, err = c.Entities.GetEntity(r.Context(), e.ID); err != nil {
				fail(http.StatusInternalServerError, seoFields{}, "The entry could not be loaded.")
				return
			}
		}
		res, err := applySEOEdit(attrsMap(e.Attributes), ed, postEditorFor(sc))
		var le *seoLengthError
		if errors.As(err, &le) {
			fail(http.StatusUnprocessableEntity, res.Current, sentence(le.Error()))
			return
		}
		if len(res.Changed) > 0 {
			b, _ := json.Marshal(res.Attrs)
			raw := json.RawMessage(b)
			summary := "Updated SEO in the list"
			updated, err := c.Entities.UpdateEntity(r.Context(), e.ID, actorID(r), &entity.UpdateEntityInput{Attributes: &raw, ChangeSummary: &summary, ExpectedVersion: &e.Version})
			if errors.Is(err, entity.ErrVersionConflict) {
				continue
			}
			if err != nil {
				fail(http.StatusUnprocessableEntity, seoFieldsOf(attrsMap(e.Attributes)), entityErrorText(err))
				return
			}
			a.auditEntity(r, auditEntityUpdate, e.ID, updated, map[string]any{"schema": sc.Slug, "bulk": false, "inline": true, "fields": res.Changed})
		}
		out := seoInlineReply{Status: "saved", Values: res.Current, Counters: seoCounters(res.Current), Message: "Saved SEO for " + e.Name + "."}
		status := http.StatusOK
		if len(res.Conflicts) > 0 {
			out.Status, out.Conflicts, status = "conflict", res.Conflicts, http.StatusConflict
			out.Message = "Changed elsewhere: " + strings.Join(slices.Sorted(maps.Keys(res.Conflicts)), ", ") + ". Retry keeps your value."
		}
		reply(status, out)
		return
	}
	fail(http.StatusConflict, seoFieldsOf(attrsMap(e.Attributes)), "The entry changed while saving. Try again.")
}
