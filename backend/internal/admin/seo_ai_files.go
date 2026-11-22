package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/seo"
)

const seoAIFilesPath = "/settings/seo/ai-files"

// aiFilesRecent is how many recent changes the card lists.
const aiFilesRecent = 5

// AIFilesStatus is what the AI files tab shows about the two built files.
type AIFilesStatus struct {
	LLMs, Full AIFileStatus
	// Changes are the recorded changes, newest first.
	Changes []AIFileChange
}

// AIFileStatus is one built file. A nil ChangedAt means it was never built.
type AIFileStatus struct {
	ChangedAt *time.Time
	Bytes     int
}

// AIFileChange is one line of "Recently changed".
type AIFileChange struct {
	At   time.Time
	Text string
}

// aiFilesState is what the card edits, as stored or as submitted.
type aiFilesState struct {
	Set seo.AIFiles
	// Posts is the typed text, kept so a rejected value is shown back as typed.
	Posts string
	Errs  []ui.FieldError
}

type seoAIFilesData struct {
	Summary []ui.FieldError
	Errs    map[string]string
	State   aiFilesState
	Files   ui.Table
	Changes ui.Table
	MaxSum  int
}

func aiFilesStored(set seo.Settings) aiFilesState {
	return aiFilesState{Set: set.AIFiles, Posts: strconv.Itoa(set.AIFiles.EffectivePosts())}
}

// aiFilesFromForm applies the posted fields over stored. Fields the post does
// not carry keep their stored value (a switch posts "true" or "false").
func aiFilesFromForm(form url.Values, stored aiFilesState) aiFilesState {
	next := aiFilesState{Set: stored.Set, Posts: stored.Posts}
	if form.Has("ai_llms") {
		next.Set.LLMsOff = form.Get("ai_llms") != "true"
	}
	if form.Has("ai_full") {
		next.Set.FullOff = form.Get("ai_full") != "true"
	}
	if form.Has("ai_summary") {
		next.Set.Summary = strings.TrimSpace(form.Get("ai_summary"))
		if utf8.RuneCountInString(next.Set.Summary) > seo.MaxAISummaryRunes {
			next.Errs = append(next.Errs, ui.FieldError{ID: "ai-summary", Label: "Summary override", Message: fmt.Sprintf("The summary can be at most %d characters.", seo.MaxAISummaryRunes)})
		}
	}
	if form.Has("ai_posts") {
		next.Posts = strings.TrimSpace(form.Get("ai_posts"))
		next.Set.Posts = 0
		if next.Posts != "" {
			n, err := strconv.Atoi(next.Posts)
			if err != nil || n < 1 || n > seo.MaxAIPosts {
				next.Errs = append(next.Errs, ui.FieldError{ID: "ai-posts", Label: "Posts listed in llms.txt", Message: fmt.Sprintf("Enter a whole number from 1 to %d.", seo.MaxAIPosts)})
			} else if n != seo.DefaultAIPosts {
				next.Set.Posts = n // the default itself stays unstored
			}
		}
	}
	return next
}

func (a *Admin) handleSEOAIFiles(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	a.renderSEOAIFiles(w, r, http.StatusOK, aiFilesStored(set), nil)
}

func (a *Admin) handleSEOAIFilesSave(w http.ResponseWriter, r *http.Request) {
	set, ok := a.seoLoad(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and keeps the stored values
	next := aiFilesFromForm(r.PostForm, aiFilesStored(set))
	if len(next.Errs) > 0 {
		a.renderSEOAIFiles(w, r, http.StatusUnprocessableEntity, next, next.Errs)
		return
	}
	n := next.Set
	if !a.seoSave(w, r, func(s *seo.Settings) { s.AIFiles = n }, map[string]any{"tab": "ai-files", "llms": !n.LLMsOff, "full": !n.FullOff}) {
		return
	}
	a.SetFlash(w, r, "success", "SEO settings saved.")
	http.Redirect(w, r, a.adminPath+seoAIFilesPath, http.StatusSeeOther)
}

func fmtKB(bytes int) string {
	if bytes <= 0 {
		return ""
	}
	return strconv.Itoa((bytes+1023)/1024) + " KB"
}

// aiFilesTables builds the read-only Files and Recently changed tables.
func (a *Admin) aiFilesTables(r *http.Request) (files, changes ui.Table) {
	var st AIFilesStatus
	if a.deps.AIFiles != nil {
		var err error
		if st, err = a.deps.AIFiles(r.Context()); err != nil {
			a.logger().Error("ai files status", "err", err)
			st = AIFilesStatus{}
		}
	}
	files = ui.Table{
		ID: "ai-files", Caption: "Files",
		Columns: []ui.Col{{Key: "file", Label: "File"}, {Key: "what", Label: "What it is"}, {Key: "updated", Label: "Last updated"}},
	}
	row := func(id, what string, f AIFileStatus, size bool) {
		updated := ui.Cell{Text: "Not built yet"}
		if f.ChangedAt != nil {
			updated = ui.Cell{Text: fmtDateTime(f.ChangedAt.UTC()), Nowrap: true}
		}
		if size && f.Bytes > 0 {
			what += " (" + fmtKB(f.Bytes) + ")"
		}
		files.Rows = append(files.Rows, ui.Row{ID: id, Label: id, Cells: []ui.Cell{{Text: id, Mono: true}, {Text: what}, updated},
			Actions: []ui.Action{{Label: "View", Href: "/" + id, NewTab: true, Button: true}}})
	}
	row("llms.txt", "Site summary and key pages", st.LLMs, false)
	row("llms-full.txt", "Every page as Markdown", st.Full, true)

	changes = ui.Table{
		ID: "ai-changes", Caption: "Recently changed",
		Columns: []ui.Col{{Key: "change", Label: "Change"}, {Key: "when", Label: "When"}},
		Empty:   ui.Empty{Icon: "seo", Title: "No changes recorded yet", Body: "Changes to pages, people and the site summary appear here once the files are read."},
	}
	for i, c := range st.Changes {
		if i == aiFilesRecent {
			break
		}
		changes.Rows = append(changes.Rows, ui.Row{ID: strconv.Itoa(i), Label: c.Text, Cells: []ui.Cell{{Text: c.Text}, {Text: fmtDateTime(c.At.UTC()), Nowrap: true}}})
	}
	return files, changes
}

func (a *Admin) renderSEOAIFiles(w http.ResponseWriter, r *http.Request, status int, st aiFilesState, errs []ui.FieldError) {
	d := seoAIFilesData{Summary: errs, Errs: map[string]string{}, State: st, MaxSum: seo.MaxAISummaryRunes}
	for _, e := range errs {
		d.Errs[e.ID] = e.Message
	}
	d.Files, d.Changes = a.aiFilesTables(r)
	v := a.NewView(w, r, "SEO")
	v.Section, v.Tab = "seo", "seo-ai-files"
	v.Width = "narrow"
	v.Data = d
	a.Render(w, r, status, "settings/seo-ai-files", v)
}
