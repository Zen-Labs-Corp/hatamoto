package admin

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/entitycsv"
	"hatamoto.dev/core/internal/httpx"
)

const (
	csvBase = "/settings/import/csv"
	// csvBodyLimit bounds the upload: the file and a little more. The other CSV forms are small.
	csvBodyLimit = entitycsv.MaxFileBytes + 1<<20
	csvFormLimit = 1 << 20
	csvRunWindow = 30 * time.Minute
)

var csvSteps = []string{"Upload", "Map columns", "Dry run", "Report"}

func csvStepList(current int) []ui.Step {
	out := make([]ui.Step, len(csvSteps))
	for i, l := range csvSteps {
		out[i] = ui.Step{Label: l, Current: i == current, Done: i < current}
	}
	return out
}

func (a *Admin) mountImportCSV(owner *httpx.Group) {
	owner.HandleFunc("GET "+csvBase, a.handleCSVUploadForm)
	owner.HandleFunc("POST "+csvBase, a.handleCSVUpload)
	owner.HandleFunc("GET "+csvBase+"/{id}", a.handleCSVMapForm)
	owner.HandleFunc("POST "+csvBase+"/{id}/map", a.handleCSVMap)
	owner.HandleFunc("GET "+csvBase+"/{id}/dry-run", a.handleCSVDryRun)
	owner.HandleFunc("POST "+csvBase+"/{id}/run", a.handleCSVRun)
	owner.HandleFunc("GET "+csvBase+"/{id}/report", a.handleCSVReport)
}

// csvReady answers the unavailable page itself when the import is not wired.
func (a *Admin) csvReady(w http.ResponseWriter, r *http.Request) (*entitycsv.Spool, bool) {
	c := a.deps.Content
	if c.EntityImport == nil || c.Entities == nil || c.CSVSpoolDir == "" {
		a.unavailable(w, r, "The CSV import")
		return nil, false
	}
	spool := &entitycsv.Spool{Dir: c.CSVSpoolDir}
	spool.Sweep() // abandoned uploads go on the next wizard request, not only on the next upload
	return spool, true
}

func (a *Admin) csvURL(id uuid.UUID, tail string) string {
	return a.adminPath + csvBase + "/" + id.String() + tail
}

// csvUpload loads the upload named in the path with its state, answering the
// error page itself.
func (a *Admin) csvUpload(w http.ResponseWriter, r *http.Request) (*entitycsv.Spool, uuid.UUID, *entitycsv.State, bool) {
	spool, ok := a.csvReady(w, r)
	if !ok {
		return nil, uuid.Nil, nil, false
	}
	notFound := func() {
		a.ErrorBack(w, r, http.StatusNotFound, "Upload not found", "That upload does not exist or was removed. Uploads are kept for a day.", a.adminPath+csvBase, "Back to the CSV import")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		notFound()
		return nil, uuid.Nil, nil, false
	}
	st, err := spool.LoadState(id)
	if errors.Is(err, entitycsv.ErrNoUpload) {
		notFound()
		return nil, uuid.Nil, nil, false
	}
	if err != nil {
		a.serverError(w, r, "The upload could not be loaded", err)
		return nil, uuid.Nil, nil, false
	}
	return spool, id, st, true
}

// csvSchema finds the importable type with this slug.
func (a *Admin) csvSchema(r *http.Request, slug string) *entity.EntitySchema {
	return a.orderSchema(r, slug)
}

// ---- step 1: upload ----

type csvUploadData struct {
	Steps   []ui.Step
	Types   []ui.Option
	Errors  map[string]string
	Summary []ui.FieldError
	Action  string
	Back    string
	MaxMB   int
	MaxRows int
}

// csvCrumbs is Settings, Import & export, CSV import, then the step. The CSV
// wizard lives under the WordPress import tab's path but is not that page.
func (a *Admin) csvCrumbs(step string) []Crumb {
	trail := a.areaCrumbs("import")
	trail = trail[:len(trail)-1]
	if step == "" {
		return append(trail, Crumb{Label: "CSV import"})
	}
	return append(trail, Crumb{Label: "CSV import", Href: a.adminPath + csvBase}, Crumb{Label: step})
}

func (a *Admin) renderCSVUpload(w http.ResponseWriter, r *http.Request, status int, selected string, errs map[string]string, summary []ui.FieldError) {
	schemas, err := a.deps.Content.Entities.ListSchemas(r.Context())
	if err != nil {
		a.serverError(w, r, "The content types could not be loaded", err)
		return
	}
	d := csvUploadData{Steps: csvStepList(0), Errors: errs, Summary: summary, Action: a.adminPath + csvBase,
		Back: a.adminPath + "/settings/import", MaxMB: entitycsv.MaxFileBytes >> 20, MaxRows: entitycsv.MaxRows}
	for _, sc := range schemas {
		if !entity.IsReuseSchema(sc) {
			d.Types = append(d.Types, ui.Option{Value: sc.Slug, Label: sc.Name, Selected: sc.Slug == selected})
		}
	}
	v := a.NewView(w, r, "Import a CSV file")
	v.Crumbs = a.csvCrumbs("")
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "import/csv-upload", v)
}

func (a *Admin) handleCSVUploadForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.csvReady(w, r); !ok {
		return
	}
	a.renderCSVUpload(w, r, http.StatusOK, r.URL.Query().Get("type"), nil, nil)
}

func (a *Admin) handleCSVUpload(w http.ResponseWriter, r *http.Request) {
	spool, ok := a.csvReady(w, r)
	if !ok {
		return
	}
	fail := func(field, label, msg string) {
		a.renderCSVUpload(w, r, http.StatusUnprocessableEntity, r.PostFormValue("type"),
			map[string]string{field: msg}, []ui.FieldError{{ID: field, Label: label, Message: msg}})
	}
	if err := parseImportUpload(w, r); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail("file", "CSV file", fmt.Sprintf("The file is larger than %d MiB. Split it and import the parts one after another.", entitycsv.MaxFileBytes>>20))
			return
		}
		fail("file", "CSV file", "The upload could not be read. Check the connection and try again.")
		return
	}
	sc := a.csvSchema(r, r.PostFormValue("type"))
	if sc == nil {
		fail("type", "Content type", "Choose the content type the rows become.")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		fail("file", "CSV file", "Choose a CSV file to upload.")
		return
	}
	defer f.Close()
	id, err := spool.Save(f, entitycsv.MaxFileBytes)
	switch {
	case errors.Is(err, entitycsv.ErrFileEmpty), errors.Is(err, entitycsv.ErrFileTooLarge):
		fail("file", "CSV file", sentence(err.Error()))
		return
	case err != nil:
		a.serverError(w, r, "The upload could not be stored", err)
		return
	}
	file, err := spool.Open(id)
	if err != nil {
		a.serverError(w, r, "The upload could not be read", err)
		return
	}
	header, sample, rows, err := entitycsv.Preview(file)
	file.Close()
	if err != nil {
		_ = spool.Remove(id)
		fail("file", "CSV file", sentence(err.Error()))
		return
	}
	st := &entitycsv.State{Type: sc.Slug, FileName: filepath.Base(hdr.Filename), Header: header, Sample: sample, Rows: rows}
	if err := spool.SaveState(id, st); err != nil {
		a.serverError(w, r, "The upload could not be stored", err)
		return
	}
	http.Redirect(w, r, a.csvURL(id, ""), http.StatusSeeOther)
}

// ---- step 2: map ----

type csvMapRow struct {
	ID      string
	Header  string
	Sample  []string
	Options []ui.Option
}

type csvMapData struct {
	Steps    []ui.Step
	Action   string
	Back     string
	Title    string
	Type     string
	FileName string
	Rows     int
	Lines    []csvMapRow
	Status   []ui.Option
	Summary  []ui.FieldError
}

func (a *Admin) renderCSVMap(w http.ResponseWriter, r *http.Request, status int, id uuid.UUID, st *entitycsv.State, sc *entity.EntitySchema, chosen map[string]string, def entity.EntityStatus, summary []ui.FieldError) {
	attrs := entitycsv.AttributeColumns(sc.Schema)
	targets := entitycsv.Targets(attrs)
	d := csvMapData{Steps: csvStepList(1), Action: a.csvURL(id, "/map"), Back: a.adminPath + csvBase + "?type=" + sc.Slug,
		Title: "Map the columns", Type: sc.Name, FileName: st.FileName, Rows: st.Rows, Summary: summary}
	for i, h := range st.Header {
		row := csvMapRow{ID: fmt.Sprintf("col-%d", i), Header: h}
		for _, s := range st.Sample {
			if i < len(s) {
				row.Sample = append(row.Sample, s[i])
			} else {
				row.Sample = append(row.Sample, "")
			}
		}
		for _, t := range targets {
			row.Options = append(row.Options, ui.Option{Value: t.ID, Label: t.Label, Selected: t.ID == chosen[h]})
		}
		d.Lines = append(d.Lines, row)
	}
	for _, s := range []entity.EntityStatus{entity.StatusDraft, entity.StatusPublished, entity.StatusScheduled, entity.StatusArchived} {
		d.Status = append(d.Status, ui.Option{Value: string(s), Label: strings.ToUpper(string(s[:1])) + string(s[1:]), Selected: s == def})
	}
	v := a.NewView(w, r, d.Title)
	v.Crumbs = a.csvCrumbs(d.Title)
	v.Data = d
	a.Render(w, r, status, "import/csv-map", v)
}

func (a *Admin) handleCSVMapForm(w http.ResponseWriter, r *http.Request) {
	_, id, st, ok := a.csvUpload(w, r)
	if !ok {
		return
	}
	sc := a.csvSchema(r, st.Type)
	if sc == nil {
		a.notFound(w, r, "Entity type", a.adminPath+csvBase, "Back to the CSV import")
		return
	}
	chosen, def := entitycsv.Suggest(st.Header, entitycsv.AttributeColumns(sc.Schema)), entity.StatusDraft
	if st.Mapping != nil {
		chosen, def = st.Mapping.Cols, st.Mapping.Status
	}
	a.renderCSVMap(w, r, http.StatusOK, id, st, sc, chosen, def, nil)
}

func (a *Admin) handleCSVMap(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, csvFormLimit)
	spool, id, st, ok := a.csvUpload(w, r)
	if !ok {
		return
	}
	sc := a.csvSchema(r, st.Type)
	if sc == nil {
		a.notFound(w, r, "Entity type", a.adminPath+csvBase, "Back to the CSV import")
		return
	}
	m := entitycsv.Mapping{Type: st.Type, Cols: map[string]string{}, Status: entity.EntityStatus(r.PostFormValue("status"))}
	if m.Status == "" {
		m.Status = entity.StatusDraft
	}
	for i, h := range st.Header {
		m.Cols[h] = r.PostFormValue(fmt.Sprintf("col-%d", i))
	}
	if err := m.Validate(st.Header, entitycsv.AttributeColumns(sc.Schema)); err != nil {
		a.renderCSVMap(w, r, http.StatusUnprocessableEntity, id, st, sc, m.Cols, m.Status,
			[]ui.FieldError{{ID: "col-0", Label: "Columns", Message: sentence(err.Error())}})
		return
	}
	st.Mapping, st.Dry, st.Done = &m, nil, nil
	if err := spool.SaveState(id, st); err != nil {
		a.serverError(w, r, "The mapping could not be stored", err)
		return
	}
	http.Redirect(w, r, a.csvURL(id, "/dry-run"), http.StatusSeeOther)
}

// ---- step 3: dry run ----

type csvResultData struct {
	Steps      []ui.Step
	Title      string
	Type       string
	FileName   string
	Summary    string
	Errors     []entitycsv.RowError
	More       int
	Importable int
	Back       string
	Run        string
	List       string
	Done       bool
}

func csvSummary(rep *entitycsv.Report, done bool) string {
	rows := fmt.Sprintf("%d %s", rep.Rows, plural(rep.Rows, "row", "rows"))
	same := ""
	if rep.Unchanged > 0 {
		same = fmt.Sprintf(", %d unchanged", rep.Unchanged)
	}
	if done {
		return fmt.Sprintf("%s: %d created, %d updated%s, %d failed.", rows, rep.Created, rep.Updated, same, rep.Failed)
	}
	return fmt.Sprintf("%s: %d will be created, %d updated%s, %d have %s.", rows, rep.Created, rep.Updated, same, rep.Failed, plural(rep.Failed, "an error", "errors"))
}

func (a *Admin) csvResult(id uuid.UUID, st *entitycsv.State, rep *entitycsv.Report, step int, done bool) csvResultData {
	d := csvResultData{Steps: csvStepList(step), Type: st.Type, FileName: st.FileName, Summary: csvSummary(rep, done),
		Errors: rep.Errors, More: rep.Failed - len(rep.Errors), Importable: rep.Created + rep.Updated, Done: done,
		Back: a.csvURL(id, ""), Run: a.csvURL(id, "/run"), List: a.adminPath + "/entities?type=" + st.Type}
	if done {
		d.Back = a.adminPath + csvBase + "?type=" + st.Type // the file is gone: start again
	}
	if d.More < 0 {
		d.More = 0
	}
	return d
}

func (a *Admin) handleCSVDryRun(w http.ResponseWriter, r *http.Request) {
	spool, id, st, ok := a.csvUpload(w, r)
	if !ok {
		return
	}
	if st.Done != nil {
		http.Redirect(w, r, a.csvURL(id, "/report"), http.StatusSeeOther)
		return
	}
	sc := a.csvSchema(r, st.Type)
	if st.Mapping == nil || sc == nil {
		http.Redirect(w, r, a.csvURL(id, ""), http.StatusSeeOther)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(csvRunWindow))
	f, err := spool.Open(id)
	if err != nil {
		a.serverError(w, r, "The upload could not be read", err)
		return
	}
	defer f.Close()
	rep, err := entitycsv.Run(r.Context(), a.deps.Content.EntityImport, a.csvActor(r), sc, *st.Mapping, f, true)
	if err != nil {
		a.csvRunFailed(w, r, id, "The file could not be checked", err)
		return
	}
	st.Dry = rep
	if err := spool.SaveState(id, st); err != nil {
		a.serverError(w, r, "The dry run could not be stored", err)
		return
	}
	v := a.NewView(w, r, "Dry run")
	v.Crumbs = a.csvCrumbs(v.Title)
	v.Data = a.csvResult(id, st, rep, 2, false)
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "import/csv-dryrun", v)
}

func (a *Admin) csvActor(r *http.Request) *uuid.UUID {
	if u := userFrom(r.Context()); u != nil {
		if id, err := uuid.Parse(u.ID); err == nil {
			return &id
		}
	}
	return nil
}

// csvRunFailed shows why a run stopped and links back to the mapping. What a
// real run already stored stays; running it again updates those rows.
func (a *Admin) csvRunFailed(w http.ResponseWriter, r *http.Request, id uuid.UUID, what string, err error) {
	a.logger().Error(what, "err", err)
	a.ErrorBack(w, r, http.StatusUnprocessableEntity, what, sentence(err.Error()), a.csvURL(id, ""), "Back to the mapping")
}

// ---- step 4: import and report ----

func (a *Admin) handleCSVRun(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, csvFormLimit)
	spool, id, st, ok := a.csvUpload(w, r)
	if !ok {
		return
	}
	if st.Done != nil {
		http.Redirect(w, r, a.csvURL(id, "/report"), http.StatusSeeOther)
		return
	}
	sc := a.csvSchema(r, st.Type)
	if st.Mapping == nil || sc == nil {
		http.Redirect(w, r, a.csvURL(id, ""), http.StatusSeeOther)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(csvRunWindow))
	_ = rc.SetReadDeadline(time.Now().Add(csvRunWindow))
	f, err := spool.Open(id)
	if err != nil {
		a.serverError(w, r, "The upload could not be read", err)
		return
	}
	rep, err := entitycsv.Run(r.Context(), a.deps.Content.EntityImport, a.csvActor(r), sc, *st.Mapping, f, false)
	f.Close()
	if err != nil {
		a.csvRunFailed(w, r, id, "The import stopped", err)
		return
	}
	st.Done = rep
	if err := spool.SaveState(id, st); err != nil {
		a.logger().Error("csv import: report not stored", "err", err)
	}
	_ = spool.Remove(id)
	a.audit(r, "entity.import_csv", st.Type, map[string]any{
		"created": rep.Created, "updated": rep.Updated, "unchanged": rep.Unchanged, "failed": rep.Failed, "rows": rep.Rows, "file": st.FileName,
	})
	http.Redirect(w, r, a.csvURL(id, "/report"), http.StatusSeeOther)
}

func (a *Admin) handleCSVReport(w http.ResponseWriter, r *http.Request) {
	_, id, st, ok := a.csvUpload(w, r)
	if !ok {
		return
	}
	if st.Done == nil {
		http.Redirect(w, r, a.csvURL(id, ""), http.StatusSeeOther)
		return
	}
	v := a.NewView(w, r, "CSV import report")
	v.Crumbs = a.csvCrumbs(v.Title)
	v.Data = a.csvResult(id, st, st.Done, 3, true)
	v.Width = "narrow"
	a.Render(w, r, http.StatusOK, "import/csv-report", v)
}
