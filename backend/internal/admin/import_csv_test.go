package admin

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/entity"
)

func init() {
	areaPages["importcsv"] = []string{"import/csv-upload", "import/csv-map", "import/csv-dryrun", "import/csv-report"}
}

// csvImporter is an EntityImporter over a set of keys: an unseen key is
// created, a seen one updated, and the name "dup" fails like a slug clash. A
// dry run stores nothing.
type csvImporter struct {
	seen    map[string]bool
	batches []entity.ImportBatch
}

func (f *csvImporter) Import(_ context.Context, _ *uuid.UUID, b entity.ImportBatch) (*entity.ImportResult, error) {
	f.batches = append(f.batches, b)
	res := &entity.ImportResult{Items: make([]entity.ImportItemResult, len(b.Items))}
	for i, it := range b.Items {
		r := &res.Items[i]
		switch {
		case it.Name == "dup":
			r.Err = entity.ErrSlugAlreadyExists
			res.Failed++
		case f.seen[it.Key]:
			res.Updated++
		default:
			r.Created = true
			res.Created++
			if !b.DryRun {
				f.seen[it.Key] = true
			}
		}
	}
	return res, nil
}

type csvRig struct {
	*contentRig
	imp    *csvImporter
	events []AuditEvent
}

func newCSVRig(t *testing.T) *csvRig {
	t.Helper()
	r := &csvRig{imp: &csvImporter{seen: map[string]bool{}}}
	dir := t.TempDir()
	r.contentRig = newContentRigWith(t, func(d *Deps) {
		d.Content.EntityImport = r.imp
		d.Content.CSVSpoolDir = dir
		d.Audit = func(_ context.Context, e AuditEvent) { r.events = append(r.events, e) }
	})
	return r
}

func (r *csvRig) upload(t *testing.T, typ, name, content string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_csrf", csrfValue) // first part, as the form's hidden field sends it
	_ = mw.WriteField("type", typ)
	if name != "" {
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write([]byte(content))
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/admin/settings/import/csv", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	r.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""})
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

var csvIDRx = regexp.MustCompile(`/admin/settings/import/csv/([0-9a-f-]{36})`)

const csvFile = "sku,title,minutes,note\n1,Pie,30,x\n2,dup,5,y\n3,Soup,abc,z\n"

// csvUploaded uploads csvFile for the recipe type and returns the upload id.
func (r *csvRig) csvUploaded(t *testing.T) string {
	t.Helper()
	rec := r.upload(t, "recipe", "recipes.csv", csvFile)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d\n%s", rec.Code, rec.Body)
	}
	m := csvIDRx.FindStringSubmatch(rec.Header().Get("Location"))
	if m == nil {
		t.Fatalf("redirect = %q", rec.Header().Get("Location"))
	}
	return m[1]
}

// csvPage fetches a wizard page and checks it like contentRig.get, with the
// random upload id replaced so the golden is stable. It returns the raw body
// (select options included) for assertions.
func (r *csvRig) csvPage(t *testing.T, path, id, golden string) string {
	t.Helper()
	rec := r.do("GET", path, "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET %s = %d\n%s", path, rec.Code, rec.Body)
	}
	raw := strings.ReplaceAll(rec.Body.String(), id, "UPLOAD-ID")
	body := normalizePage(raw)
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("%s a11y: %s", path, strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/"+golden+".html", body)
	return raw
}

func csvMapForm(id string) url.Values {
	return url.Values{"col-0": {"key"}, "col-1": {"name"}, "col-2": {"attr:minutes"}, "col-3": {"ignore"}, "status": {"draft"}}
}

func TestCSVImportWizardGoldens(t *testing.T) {
	t.Parallel()
	r := newCSVRig(t)
	r.get(t, "owner-token", "/admin/settings/import/csv?type=recipe", "import-csv-upload")
	id := r.csvUploaded(t)
	base := "/admin/settings/import/csv/" + id
	body := r.csvPage(t, base, id, "import-csv-map")
	has(t, body, `name="col-0"`, `aria-label="Map sku to"`, `<option value="attr:minutes" selected>`, `<option value="ignore" selected>`, "recipes.csv", "Pie")

	rec := r.post(base+"/map", csvMapForm(id))
	if rec.Code != 303 || rec.Header().Get("Location") != base+"/dry-run" {
		t.Fatalf("map = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	body = r.csvPage(t, base+"/dry-run", id, "import-csv-dryrun")
	has(t, body, "3 rows: 1 will be created, 0 updated, 2 have errors.", "<caption", `scope="row" class="hm-table__rowhead">3<`, `minutes`, `&#34;abc&#34; is not a number`, "Import 1 row")
	if len(r.events) != 0 {
		t.Errorf("a dry run wrote an audit entry: %+v", r.events)
	}
	if len(r.imp.seen) != 0 {
		t.Error("a dry run stored rows")
	}

	rec = r.post(base+"/run", url.Values{})
	if rec.Code != 303 || rec.Header().Get("Location") != base+"/report" {
		t.Fatalf("run = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	body = r.csvPage(t, base+"/report", id, "import-csv-report")
	has(t, body, "3 rows: 1 created, 0 updated, 2 failed.", "Open the list")
	if len(r.events) != 1 || r.events[0].Action != "entity.import_csv" || r.events[0].Target != "recipe" ||
		r.events[0].Detail["created"] != 1 || r.events[0].Detail["failed"] != 2 || r.events[0].Detail["file"] != "recipes.csv" {
		t.Errorf("audit = %+v", r.events)
	}
	last := r.imp.batches[len(r.imp.batches)-1]
	if last.DryRun || last.Source != "csv" || len(last.Items) != 2 || last.Items[0].Key != "csv:recipe:1" || last.Items[0].Status != entity.StatusDraft {
		t.Errorf("real batch = %+v", last)
	}
	// The report survives a reload, a second run does not import twice, the file is gone.
	if rec := r.post(base+"/run", url.Values{}); rec.Code != 303 || rec.Header().Get("Location") != base+"/report" {
		t.Errorf("second run = %d", rec.Code)
	}
	if n := len(r.events); n != 1 {
		t.Errorf("audit entries = %d after a second run", n)
	}
}

func TestCSVImportWizardReusesStateAndRejects(t *testing.T) {
	t.Parallel()
	r := newCSVRig(t)
	for name, tc := range map[string]struct{ typ, file, content, want string }{
		"no file":     {"recipe", "", "", "Choose a CSV file"},
		"empty file":  {"recipe", "e.csv", "", "The file is empty"},
		"no type":     {"nope", "a.csv", "a\n1\n", "Choose the content type"},
		"reuse type":  {"block", "a.csv", "a\n1\n", "Choose the content type"},
		"duplicate":   {"recipe", "a.csv", "a,a\n1,2\n", "names"},
		"header only": {"recipe", "a.csv", "a,b\n", "no rows"},
	} {
		rec := r.upload(t, tc.typ, tc.file, tc.content)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), tc.want) || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d, want 422 with %q", name, rec.Code, tc.want)
		}
	}

	id := r.csvUploaded(t)
	base := "/admin/settings/import/csv/" + id
	// Without Key and Name the mapping is refused and shown again with the choices.
	bad := csvMapForm(id)
	bad.Set("col-0", "ignore")
	rec := r.post(base+"/map", bad)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Map one column to Key") {
		t.Errorf("mapping without key = %d\n%s", rec.Code, rec.Body)
	}
	bad = csvMapForm(id)
	bad.Set("col-3", "name")
	if rec := r.post(base+"/map", bad); rec.Code != 422 {
		t.Errorf("target used twice = %d", rec.Code)
	}
	// The dry run before any mapping goes back to the map step.
	if rec := r.do("GET", base+"/dry-run", "owner-token", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != base {
		t.Errorf("dry run without a mapping = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, p := range []string{"/admin/settings/import/csv/" + uuid.NewString(), "/admin/settings/import/csv/not-an-id", "/admin/settings/import/csv/" + uuid.NewString() + "/report"} {
		if rec := r.do("GET", p, "owner-token", nil, nil); rec.Code != 404 {
			t.Errorf("GET %s = %d", p, rec.Code)
		}
	}
}

func TestCSVImportIsOwnerOnlyAndBounded(t *testing.T) {
	t.Parallel()
	r := newCSVRig(t)
	for _, p := range []string{"/admin/settings/import/csv", "/admin/settings/import/csv/" + uuid.NewString()} {
		if rec := r.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d", p, rec.Code)
		}
	}
	if rec := r.do("POST", "/admin/settings/import/csv", "editor-token", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("editor upload = %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/admin/settings/import/csv", strings.NewReader("x"))
	req.ContentLength = csvBodyLimit + 1
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	r.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""})
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload = %d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/admin/settings/import/csv/"+uuid.NewString()+"/map", strings.NewReader("x"))
	req.ContentLength = csvFormLimit + 1
	req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: "owner-token"})
	r.addFetchMeta(req, "owner-token", map[string]string{"X-CSRF-Token": ""})
	rec = httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized form = %d", rec.Code)
	}
	// Off when the importer is not wired.
	off := newContentRig(t)
	if rec := off.do("GET", "/admin/settings/import/csv", "owner-token", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unwired = %d", rec.Code)
	}
}

func TestImportSourceLinksToCSV(t *testing.T) {
	g, _, _ := importRig(t)
	has(t, g.do("GET", "/admin/settings/import", "owner-token", nil, nil).Body.String(), `href="/admin/settings/import/csv"`, "Import a CSV file instead")
}

func TestCSVSuggestedMappingRoundTrip(t *testing.T) {
	t.Parallel()
	r := newCSVRig(t)
	rec := r.upload(t, "recipe", "x.csv", "Name,Key,minutes,order,id\nPie,a,5,1,abc\n")
	id := csvIDRx.FindStringSubmatch(rec.Header().Get("Location"))[1]
	body := r.do("GET", "/admin/settings/import/csv/"+id, "owner-token", nil, nil).Body.String()
	// An exported file maps itself: Key and Name are found, order and id are ignored.
	if !strings.Contains(body, `<option value="key" selected>`) || !strings.Contains(body, `<option value="name" selected>`) || !strings.Contains(body, `<option value="attr:minutes" selected>`) {
		t.Errorf("suggested mapping missing:\n%s", body)
	}
}
