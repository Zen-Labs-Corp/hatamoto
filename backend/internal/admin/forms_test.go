package admin

import (
	"context"
	"encoding/csv"
	"errors"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/spam"
)

type fakeForms struct {
	mu    sync.Mutex
	forms map[uuid.UUID]*form.Form
	subs  map[uuid.UUID]*form.FormSubmission
	// calls counts ListSubmissions pages, to prove the export pages through.
	listCalls int
	// saves counts SaveDefinition writes and drives the fake updated_at.
	saves int
}

func newFakeForms() *fakeForms {
	return &fakeForms{forms: map[uuid.UUID]*form.Form{}, subs: map[uuid.UUID]*form.FormSubmission{}}
}

func (f *fakeForms) addForm(name string, fields ...form.FormField) *form.Form {
	fm := &form.Form{ID: uuid.New(), Name: name, Slug: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Fields: fields, Status: form.StatusActive}
	f.forms[fm.ID] = fm
	return fm
}

func (f *fakeForms) addSub(fm *form.Form, status string, at time.Time, data map[string]any) *form.FormSubmission {
	s := &form.FormSubmission{ID: uuid.New(), FormID: fm.ID, Data: data, Status: status, CreatedAt: at}
	f.subs[s.ID] = s
	return s
}

// ListForms orders by name like the repository, so a capped scan cuts the same
// forms on every run instead of whichever the map yields last.
func (f *fakeForms) ListForms(context.Context, string) ([]*form.Form, error) {
	var out []*form.Form
	for _, fm := range f.forms {
		out = append(out, fm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (f *fakeForms) GetForm(_ context.Context, id uuid.UUID) (*form.Form, error) {
	if fm, ok := f.forms[id]; ok {
		return fm, nil
	}
	return nil, form.ErrFormNotFound
}
func (f *fakeForms) CreateForm(_ context.Context, in *form.CreateFormInput) (*form.Form, error) {
	for _, fm := range f.forms {
		if fm.Slug == in.Slug {
			return nil, form.ErrSlugAlreadyExists
		}
	}
	fm := f.addForm(in.Name)
	if in.Slug != "" {
		fm.Slug = in.Slug
	}
	fm.Description, fm.Settings, fm.Fields = in.Description, in.Settings, in.Fields
	if in.Status != "" {
		fm.Status = in.Status
	}
	return fm, nil
}
func (f *fakeForms) UpdateForm(_ context.Context, id uuid.UUID, in *form.UpdateFormInput) (*form.Form, error) {
	fm, ok := f.forms[id]
	if !ok {
		return nil, form.ErrFormNotFound
	}
	if in.Name != nil {
		fm.Name = *in.Name
	}
	if in.Description != nil {
		fm.Description = in.Description
	}
	if in.Fields != nil {
		if errs := form.ValidateDefinition(in.Fields); len(errs) > 0 {
			return nil, errs
		}
		fm.Fields = in.Fields
	}
	if in.Settings != nil {
		fm.Settings = *in.Settings
	}
	if in.Status != nil {
		fm.Status = *in.Status
	}
	return fm, nil
}

// SaveDefinition mirrors form.Service.SaveDefinition: the strict checks, then
// a compare of the base with the stored updated_at and a new updated_at.
func (f *fakeForms) SaveDefinition(_ context.Context, id uuid.UUID, base time.Time, in *form.SaveDefinitionInput) (*form.Form, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !form.ValidStatus(in.Status) {
		return nil, form.ErrInvalidStatus
	}
	if errs := form.ValidateDefinitionStrict(in.Fields); len(errs) > 0 {
		return nil, errs
	}
	fm, ok := f.forms[id]
	if !ok {
		return nil, form.ErrFormNotFound
	}
	if !fm.UpdatedAt.Equal(base) {
		return nil, form.ErrVersionConflict
	}
	fm.Name, fm.Description, fm.Status, fm.Fields, fm.Settings = in.Name, in.Description, in.Status, in.Fields, in.Settings
	f.saves++
	fm.UpdatedAt = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC).Add(time.Duration(f.saves) * time.Microsecond)
	return fm, nil
}
func (f *fakeForms) DeleteForm(_ context.Context, id uuid.UUID) error {
	if _, ok := f.forms[id]; !ok {
		return form.ErrFormNotFound
	}
	delete(f.forms, id)
	return nil
}

// submissionMatches mirrors the repository's search: a case-insensitive match
// of the text against any answer value.
func submissionMatches(s *form.FormSubmission, q string) bool {
	if q == "" {
		return true
	}
	for _, v := range s.Data {
		if strings.Contains(strings.ToLower(formatAnswer(v)), strings.ToLower(q)) {
			return true
		}
	}
	return false
}

func (f *fakeForms) ListSubmissions(_ context.Context, formID uuid.UUID, p *form.ListSubmissionsParams) ([]*form.FormSubmission, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	var all []*form.FormSubmission
	for _, s := range f.subs {
		if s.FormID == formID && (p == nil || ((p.Status == "" || s.Status == p.Status) && submissionMatches(s, p.Search))) {
			all = append(all, s)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID.String() > all[j].ID.String()
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	total := len(all)
	if p != nil && p.After != nil {
		i := 0
		for i < len(all) && !(all[i].CreatedAt.Before(p.After.CreatedAt) || (all[i].CreatedAt.Equal(p.After.CreatedAt) && all[i].ID.String() < p.After.ID.String())) {
			i++
		}
		all = all[i:]
		p = &form.ListSubmissionsParams{Limit: p.Limit}
		total = len(all)
	}
	if p != nil {
		off := min(p.Offset, total)
		end := total
		if p.Limit > 0 {
			end = min(off+p.Limit, total)
		}
		all = all[off:end]
	}
	return all, total, nil
}
func (f *fakeForms) GetSubmission(_ context.Context, id uuid.UUID) (*form.FormSubmission, error) {
	if s, ok := f.subs[id]; ok {
		c := *s
		return &c, nil
	}
	return nil, form.ErrSubmissionNotFound
}
func (f *fakeForms) UpdateSubmission(_ context.Context, id uuid.UUID, in *form.UpdateSubmissionInput) (*form.FormSubmission, error) {
	s, ok := f.subs[id]
	if !ok {
		return nil, form.ErrSubmissionNotFound
	}
	if in.Status != nil {
		s.Status = *in.Status
	}
	return s, nil
}
func (f *fakeForms) DeleteSubmission(_ context.Context, id uuid.UUID) error {
	if _, ok := f.subs[id]; !ok {
		return form.ErrSubmissionNotFound
	}
	delete(f.subs, id)
	return nil
}

type fakeFormMail struct {
	on   bool
	sent [][]string
	err  error
}

func (m *fakeFormMail) Configured() bool { return m.on }
func (m *fakeFormMail) Forward(_ context.Context, _ *form.Form, _ *form.FormSubmission, to []string, _ string) error {
	m.sent = append(m.sent, to)
	return m.err
}

func newFormRig(t *testing.T, mutate ...func(*Deps)) (*rig, *fakeForms, *fakeFormMail) {
	t.Helper()
	ff, mail := newFakeForms(), &fakeFormMail{on: true}
	g := newRig(t, append([]func(*Deps){func(d *Deps) { d.Growth.Forms, d.Growth.FormMail = ff, mail }}, mutate...)...)
	return g, ff, mail
}

var contactFields = []form.FormField{
	{ID: "a", Type: form.FieldTypeText, Label: "Name", Name: "name", Required: true},
	{ID: "b", Type: form.FieldTypeEmail, Label: "Email", Name: "email"},
	{ID: "c", Type: form.FieldTypeTextarea, Label: "Message", Name: "message"},
}

func TestFormListNewAndDelete(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Ada"})
	body := g.do("GET", "/admin/forms", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Contact") || !strings.Contains(body, `href="/admin/forms/new"`) {
		t.Fatalf("list: %s", body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("list a11y: %v", issues)
	}
	for _, want := range []string{"1 unread", "Submissions, 30 days", "Latest submissions, all forms", `data-hm-open="new-form"`, `name="template"`, "Spam protection"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(body, "Forms sections") {
		t.Errorf("list still carries the tabs row")
	}
	if ed := g.do("GET", "/admin/forms", "editor-token", nil, nil).Body.String(); strings.Contains(ed, "Spam protection") {
		t.Errorf("an editor is offered the owner-only Spam protection page")
	}
	newPage := g.do("GET", "/admin/forms/new", "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(newPage, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("new a11y: %v", issues)
	}
	if rec := g.do("POST", "/admin/forms", "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a name") {
		t.Errorf("blank name: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/forms", "owner-token", url.Values{"name": {"Newsletter"}}, nil)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/forms/design/") {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	for _, f := range ff.forms {
		if f.Name == "Newsletter" && (f.Status != form.StatusInactive || len(f.Fields) != 0) {
			t.Errorf("blank form: status %q, %d fields; want paused and empty", f.Status, len(f.Fields))
		}
	}
	if g.do("GET", "/admin/forms/delete/"+fm.ID.String(), "owner-token", nil, nil).Code != 200 || len(ff.forms) != 2 {
		t.Fatal("the confirmation page must not delete")
	}
	if rec := g.do("POST", "/admin/forms/delete/"+fm.ID.String(), "owner-token", url.Values{}, nil); rec.Code != 303 || len(ff.forms) != 1 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestFormCreateFromTemplate(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	newPage := g.do("GET", "/admin/forms/new", "owner-token", nil, nil).Body.String()
	if !strings.Contains(newPage, `name="template" value="contact"`) || !strings.Contains(newPage, `value="blank" checked`) {
		t.Errorf("new page lacks the template cards: %s", newPage)
	}
	rec := g.do("POST", "/admin/forms", "owner-token", url.Values{"template": {"contact"}}, nil)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/forms/design/") {
		t.Fatalf("create from template: %d", rec.Code)
	}
	var got *form.Form
	for _, f := range ff.forms {
		got = f
	}
	if got == nil || got.Name != "Contact" || got.Status != form.StatusInactive || len(got.Fields) != 3 {
		t.Fatalf("template form: %+v", got)
	}
	if rec := g.do("POST", "/admin/forms", "owner-token", url.Values{"template": {"nope"}, "name": {"X"}}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose one of the templates") {
		t.Errorf("unknown template: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/forms", "owner-token", url.Values{"template": {"blank"}, "name": {""}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a name") {
		t.Errorf("blank template without a name: %d", rec.Code)
	}
}

func TestFormEditUsesPublicAPIPrefix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ prefix, want, gone string }{
		{"", "/api/v1/public/forms/contact/submit", "/hook/"},
		{"/hook", "/hook/forms/contact/submit", "/api/v1/public/forms"},
	} {
		g, ff, _ := newFormRig(t, func(d *Deps) { d.PublicAPIPrefix = tc.prefix })
		fm := ff.addForm("Contact", contactFields...)
		body := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/share", "owner-token", nil, nil).Body.String()
		// the endpoint field names the API; the embed code posts to the no-JS route
		if !strings.Contains(body, `value="http`) || !strings.Contains(body, tc.want) {
			t.Errorf("prefix %q: endpoint %q missing", tc.prefix, tc.want)
		}
		if !strings.Contains(body, `action=&#34;`) || !strings.Contains(body, `/forms/contact/submit&#34;`) {
			t.Errorf("prefix %q: embed code does not post to /forms/contact/submit", tc.prefix)
		}
		if strings.Contains(body, tc.gone) {
			t.Errorf("prefix %q: page still names %q", tc.prefix, tc.gone)
		}
	}
}

func submissionRig(t *testing.T) (*rig, *fakeForms, *fakeFormMail, *form.Form, *form.FormSubmission) {
	g, ff, mail := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	s := ff.addSub(fm, form.SubmissionStatusNew, time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC),
		map[string]any{"name": "Ada <b>Lovelace</b>", "email": "ada@example.com", "message": "Hello", "legacy": "kept"})
	s.Metadata.PageURL = "https://example.com/contact"
	return g, ff, mail, fm, s
}

func TestSubmissionOldAddressesLandOnTheTab(t *testing.T) {
	t.Parallel()

	g, ff, _, fm, s := submissionRig(t)
	base := "/admin/forms/submissions"
	tab := "/admin/forms/design/" + fm.ID.String() + "/submissions"
	// The list keeps its filters; with no ?form= the first form's tab opens.
	for from, want := range map[string]string{
		base + "?form=" + fm.ID.String():                              tab,
		base + "?form=" + fm.ID.String() + "&status=new&q=ada&page=2": tab + "?page=2&q=ada&status=new",
		base + "?form=" + fm.ID.String() + "&status=bogus":            tab,
		base: tab,
	} {
		if rec := g.do("GET", from, "owner-token", nil, nil); rec.Code != 302 || rec.Header().Get("Location") != want {
			t.Errorf("GET %s: %d -> %q, want 302 -> %q", from, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	if rec := g.do("GET", base+"?form="+uuid.NewString(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown form: %d", rec.Code)
	}
	// The emailed link opens the submission on the tab and does not mark it read itself.
	for _, hdr := range []map[string]string{nil, {"Sec-Purpose": "prefetch;prerender"}} {
		rec := g.do("GET", base+"/"+s.ID.String(), "owner-token", nil, hdr)
		if rec.Code != 303 || rec.Header().Get("Location") != tab+"?s="+s.ID.String() {
			t.Errorf("submission link: %d -> %q", rec.Code, rec.Header().Get("Location"))
		}
		if ff.subs[s.ID].Status != form.SubmissionStatusNew {
			t.Fatalf("the submission link marked the submission %q", ff.subs[s.ID].Status)
		}
	}
	if rec := g.do("GET", base+"/"+uuid.NewString(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown submission: %d", rec.Code)
	}
	// Status changes, and a bad status is refused.
	g.do("POST", base+"/"+s.ID.String()+"/status", "owner-token", url.Values{"status": {"spam"}}, nil)
	if ff.subs[s.ID].Status != form.SubmissionStatusSpam {
		t.Error("status not changed")
	}
	if rec := g.do("POST", base+"/"+s.ID.String()+"/status", "owner-token", url.Values{"status": {"bogus"}}, nil); rec.Code != 400 {
		t.Errorf("bad status: %d", rec.Code)
	}
	// A site with no forms goes to the Forms list.
	g2, _, _ := newFormRig(t)
	if rec := g2.do("GET", base, "owner-token", nil, nil); rec.Code != 302 || rec.Header().Get("Location") != "/admin/forms" {
		t.Errorf("no forms: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSubmissionEmailForward(t *testing.T) {
	t.Parallel()

	g, _, mail, fm, s := submissionRig(t)
	url0 := "/admin/forms/submissions/" + s.ID.String() + "/email"
	tab := "/admin/forms/design/" + fm.ID.String() + "/submissions?s=" + s.ID.String()
	post := func(to string) int {
		rec := g.do("POST", url0, "owner-token", url.Values{"to": {to}}, nil)
		if rec.Header().Get("Location") != tab {
			t.Errorf("email %q: sent to %q, want the submission's tab", to, rec.Header().Get("Location"))
		}
		return rec.Code
	}
	if code := post("nope"); code != 303 || len(mail.sent) != 0 {
		t.Fatalf("invalid recipient: %d", code)
	}
	if code := post("a@example.com, b@example.com"); code != 303 || len(mail.sent) != 1 || len(mail.sent[0]) != 2 {
		t.Fatalf("send: %d %v", code, mail.sent)
	}
	mail.err = errors.New("smtp down")
	if code := post("a@example.com"); code != 303 {
		t.Errorf("mail failure: %d", code)
	}
	mail.on = false
	post("a@example.com")
	if len(mail.sent) != 2 {
		t.Error("nothing is sent when email is not configured")
	}
}

func TestSubmissionBulkAndDeleteAskFirst(t *testing.T) {
	t.Parallel()

	g, ff, _, fm, s := submissionRig(t)
	s2 := ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Bob"})
	other := ff.addForm("Other")
	foreign := ff.addSub(other, form.SubmissionStatusNew, time.Now(), nil)
	base := "/admin/forms/submissions"
	bulk := base + "/bulk?form=" + fm.ID.String()
	rec := g.do("POST", bulk, "owner-token", url.Values{"action": {"archive"}, "id": {s.ID.String(), s2.ID.String(), foreign.ID.String()}}, nil)
	if rec.Code != 303 || ff.subs[s.ID].Status != "archived" || ff.subs[s2.ID].Status != "archived" || ff.subs[foreign.ID].Status != "new" {
		t.Fatalf("archive: %d (foreign must be untouched)", rec.Code)
	}
	rec = g.do("POST", bulk, "owner-token", url.Values{"action": {"delete"}, "id": {s.ID.String(), s2.ID.String()}}, nil)
	if rec.Code != 200 || len(ff.subs) != 3 || !strings.Contains(rec.Body.String(), `name="confirm"`) {
		t.Fatalf("delete must ask first: %d subs=%d", rec.Code, len(ff.subs))
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("confirm a11y: %v", issues)
	}
	rec = g.do("POST", bulk, "owner-token", url.Values{"action": {"delete"}, "confirm": {"1"}, "id": {s.ID.String(), s2.ID.String()}}, nil)
	if rec.Code != 303 || len(ff.subs) != 1 {
		t.Fatalf("confirmed delete: %d subs=%d", rec.Code, len(ff.subs))
	}
	if rec := g.do("POST", bulk, "owner-token", url.Values{"action": {"read"}}, nil); rec.Code != 303 {
		t.Errorf("nothing ticked: %d", rec.Code)
	}
}

func TestSubmissionsExportStreamsEveryPageAndNeutralisesFormulas(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	n := exportPageSize*2 + 7
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		data := map[string]any{"name": "N" + strconv.Itoa(i), "email": "x@example.com", "message": "line1\nline2, \"quoted\""}
		if i == 0 {
			data["name"] = "=HYPERLINK(\"http://evil\")"
			data["extra"] = "old answer"
		}
		ff.addSub(fm, form.SubmissionStatusRead, base.Add(time.Duration(i)*time.Minute), data)
	}
	ff.addSub(fm, form.SubmissionStatusSpam, base, map[string]any{"name": "spam"})
	ff.listCalls = 0
	rec := g.do("GET", "/admin/forms/submissions/export.csv?form="+fm.ID.String()+"&status=read", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("headers: %d %v", rec.Code, rec.Header())
	}
	out := strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf")
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != n+1 {
		t.Fatalf("rows = %d, want %d (header + every read submission, no spam)", len(rows), n+1)
	}
	if ff.listCalls != 3 {
		t.Errorf("expected 3 paged reads, got %d", ff.listCalls)
	}
	if rows[0][3] != "Name" || rows[0][len(rows[0])-9] != "Other answers" {
		t.Errorf("header: %v", rows[0])
	}
	var sawFormula bool
	for _, r := range rows[1:] {
		if strings.Contains(r[3], "HYPERLINK") {
			sawFormula = true
			if !strings.HasPrefix(r[3], "'=") || !strings.Contains(r[len(r)-9], "extra: old answer") {
				t.Errorf("formula or extra not handled: %q %q", r[3], r[len(r)-9])
			}
		}
		if strings.HasPrefix(r[3], "=") {
			t.Errorf("unneutralised formula %q", r[3])
		}
	}
	if !sawFormula {
		t.Error("formula row missing")
	}
}

func TestSubmissionDrawerShowsWhereItCameFrom(t *testing.T) {
	t.Parallel()

	g, _, _, fm, s := submissionRig(t)
	url := "/admin/forms/design/" + fm.ID.String() + "/submissions?s=" + s.ID.String()
	s.Attribution = &form.Attribution{
		First: &form.Touch{Channel: "ai_assistants", Source: "chatgpt", Landing: "/post/a"},
		Last:  &form.Touch{Channel: "email", Source: "newsletter", Campaign: "oct-news", Landing: "/pricing"}}
	body := g.do("GET", url, "owner-token", nil, nil).Body.String()
	for _, want := range []string{"<dt>First touch</dt><dd>AI assistants / chatgpt / /post/a</dd>", "<dt>Last touch</dt><dd>Email / newsletter / oct-news / /pricing</dd>",
		"Ada &lt;b&gt;Lovelace", "legacy", "kept", "/contact"} {
		if !strings.Contains(body, want) {
			t.Errorf("drawer missing %q", want)
		}
	}
}

func TestSubmissionsExportCarriesAttributionColumns(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	withTouch := ff.addSub(fm, form.SubmissionStatusRead, at.Add(time.Minute), map[string]any{"name": "A"})
	withTouch.Attribution = &form.Attribution{
		First: &form.Touch{Channel: "ai_assistants", Source: "chatgpt", Landing: "/post/a"},
		Last:  &form.Touch{Channel: "email", Source: "=evil()", Landing: "/pricing"}}
	ff.addSub(fm, form.SubmissionStatusRead, at, map[string]any{"name": "B"})
	rec := g.do("GET", "/admin/forms/submissions/export.csv?form="+fm.ID.String(), "owner-token", nil, nil)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows %d, err %v", len(rows), err)
	}
	tail := func(r []string) string { return strings.Join(r[len(r)-6:], "|") }
	if got := tail(rows[0]); got != "First channel|First source|First landing|Last channel|Last source|Last landing" {
		t.Errorf("header tail = %q", got)
	}
	if got := tail(rows[1]); got != "AI assistants|chatgpt|/post/a|Email|'=evil()|/pricing" {
		t.Errorf("attributed row = %q", got)
	}
	if got := tail(rows[2]); got != "Unknown|||Unknown||" {
		t.Errorf("unattributed row = %q", got)
	}
}

func TestSubmissionDeleteAsksFirst(t *testing.T) {
	t.Parallel()

	g, ff, _, _, s := submissionRig(t)
	u := "/admin/forms/submissions/" + s.ID.String() + "/delete"
	rec := g.do("GET", u, "owner-token", nil, nil)
	if rec.Code != 200 || len(ff.subs) != 1 {
		t.Fatalf("confirm page: %d", rec.Code)
	}
	if rec := g.do("POST", u, "owner-token", url.Values{}, nil); rec.Code != 303 || len(ff.subs) != 0 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestFormEditCaptchaBanner(t *testing.T) {
	const warn = "Captcha is on for this form but no provider keys are set. Submissions are accepted unchecked."
	g, ff, _ := newFormRig(t)
	on := ff.addForm("Contact", contactFields...)
	on.Settings.EnableCaptcha = true
	off := ff.addForm("Plain", contactFields...)
	page := func(fm *form.Form, token string) string {
		rec := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", token, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("settings = %d", rec.Code)
		}
		return rec.Body.String()
	}

	// No spam service wired: nothing is known, so no banner.
	if strings.Contains(page(on, "owner-token"), warn) {
		t.Error("banner without a spam service")
	}

	svc := newFakeSpam()
	g.a.deps.Spam = svc
	owner := page(on, "owner-token")
	if !strings.Contains(owner, warn) || !strings.Contains(owner, `href="/admin/forms/spam"`) {
		t.Error("owner lacks the banner or its link with no keys")
	}
	editor := page(on, "editor-token")
	if !strings.Contains(editor, warn) || strings.Contains(editor, "/admin/forms/spam") {
		t.Error("editor should see the banner without a link")
	}
	// With the switch off the warning is in the page but hidden; the script shows it with the switch.
	if !strings.Contains(page(off, "owner-token"), `data-hm-fs-when="enable_captcha" hidden="hidden"`) || strings.Contains(owner, `data-hm-fs-when="enable_captcha" hidden`) {
		t.Error("banner is not hidden exactly while the switch is off")
	}

	svc.withSecret(spam.ProviderTurnstile, "0x4AAA-site")
	body := page(on, "owner-token")
	if strings.Contains(body, warn) {
		t.Error("banner with working keys")
	}
	if strings.Contains(body, spamTestSecret) || strings.Contains(body, "0x4AAA-site") {
		t.Error("form editor exposes a provider key")
	}
	// A secret that no longer opens is the same as no keys.
	svc.key = spam.SecretKey("rotated")
	if !strings.Contains(page(on, "owner-token"), warn) {
		t.Error("no banner for an unreadable secret")
	}
}

func TestSubmissionDaysBucketsBySiteDay(t *testing.T) {
	t.Parallel()

	tokyo := time.FixedZone("JST", 9*3600)
	now := time.Date(2026, 10, 22, 1, 0, 0, 0, tokyo) // 21 Oct 16:00 UTC
	at := func(y int, m time.Month, d, h int, loc *time.Location) *form.FormSubmission {
		return &form.FormSubmission{CreatedAt: time.Date(y, m, d, h, 30, 0, 0, loc)}
	}
	subs := []*form.FormSubmission{
		at(2026, 10, 22, 0, tokyo),     // today, just after local midnight
		at(2026, 10, 21, 23, tokyo),    // yesterday, late
		at(2026, 10, 21, 20, time.UTC), // 22 Oct 05:30 JST: today although the UTC date is the 21st
		at(2026, 10, 16, 12, tokyo),    // 6 days ago
		at(2026, 10, 15, 12, tokyo),    // 7 days ago
		at(2026, 9, 1, 12, tokyo),      // outside the window
		at(2026, 10, 23, 12, tokyo),    // the future is ignored
	}
	got := submissionDays(subs, now, 7, tokyo)
	want := []int{1, 0, 0, 0, 0, 1, 2} // 16 Oct .. 22 Oct
	if len(got) != 7 {
		t.Fatalf("len %d", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day %d = %d, want %d (%v)", i, got[i], want[i], got)
		}
	}
	for _, n := range submissionDays(nil, now, 30, tokyo) {
		if n != 0 {
			t.Fatal("empty input must give zeros")
		}
	}
}

func TestLoadRecentSubmissionsPagesStopsAndSkipsSpam(t *testing.T) {
	t.Parallel()

	ff := newFakeForms()
	fm := ff.addForm("Contact", contactFields...)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// 2 full pages plus 5 rows within the window, every 10th one spam, and old rows beyond since.
	n := submissionScanPage*2 + 5
	spam := 0
	for i := 0; i < n; i++ {
		st := form.SubmissionStatusRead
		if i%10 == 0 {
			st, spam = form.SubmissionStatusSpam, spam+1
		}
		ff.addSub(fm, st, now.Add(-time.Duration(i)*time.Minute), nil)
	}
	since := now.Add(-time.Duration(n+1) * time.Minute)
	ff.addSub(fm, form.SubmissionStatusRead, since.Add(-time.Hour), nil) // older than since: never returned
	got, total, capped, err := loadRecentSubmissions(context.Background(), ff, fm.ID, since, 5000)
	if err != nil || capped {
		t.Fatalf("err %v capped %v", err, capped)
	}
	if len(got) != n-spam {
		t.Errorf("got %d submissions, want %d (spam excluded, page boundaries crossed)", len(got), n-spam)
	}
	if total != n+1 {
		t.Errorf("total %d, want %d (first page count)", total, n+1)
	}
	for _, s := range got {
		if s.Status == form.SubmissionStatusSpam || s.CreatedAt.Before(since) {
			t.Fatalf("unexpected submission %v", s)
		}
	}
	// A cap smaller than the rows in the window stops early and says so; spam still counts toward it.
	ff.listCalls = 0
	if got, _, capped, _ := loadRecentSubmissions(context.Background(), ff, fm.ID, since, submissionScanPage); !capped || len(got) >= submissionScanPage || ff.listCalls != 1 {
		t.Errorf("cap: capped=%v rows=%d calls=%d", capped, len(got), ff.listCalls)
	}
}

func TestSubmissionsOverviewShowsStatsTrackerAndFilters(t *testing.T) {
	t.Parallel()

	g, ff, _, fm, _ := submissionRig(t)
	now := time.Now()
	ff.addSub(fm, form.SubmissionStatusRead, now.Add(-time.Minute), nil)
	for i := 0; i < 4; i++ { // a spam burst must not count as activity
		ff.addSub(fm, form.SubmissionStatusSpam, now.Add(-time.Duration(i+2)*time.Minute), nil)
	}
	body := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/submissions", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"hm-tracker", ">Submissions<", ">Unread<", ">Conversion<", ">Spam<", `id="ss-q"`, `: 1 submission, busiest"`} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if strings.Contains(body, "5 submissions") {
		t.Error("spam counted in the tracker")
	}
}

func TestFormsTabs(t *testing.T) {
	t.Parallel()

	a := &Admin{adminPath: "/admin"}
	labels := func(tabs []ui.Tab) (out []string, active string) {
		for _, tb := range tabs {
			out = append(out, tb.Label)
			if tb.Active {
				active += tb.Label
			}
		}
		return out, active
	}
	for _, tc := range []struct {
		role, active string
		want         []string
	}{
		{"owner", "submissions", []string{"Forms", "Submissions", "Spam"}},
		{"owner", "spam", []string{"Forms", "Submissions", "Spam"}},
		{"editor", "submissions", []string{"Forms", "Submissions"}},
	} {
		got, active := labels(a.formsTabs(tc.role, tc.active))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s tabs %v, want %v", tc.role, got, tc.want)
		}
		if !strings.EqualFold(active, tc.active) {
			t.Errorf("%s/%s: active tab %q", tc.role, tc.active, active)
		}
	}
}

func TestSubmissionsExportHonoursSearchAndStatus(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	at := time.Now()
	ff.addSub(fm, form.SubmissionStatusNew, at, map[string]any{"name": "=cmd|' /C calc'!A0 Match", "email": "a@example.com"})
	ff.addSub(fm, form.SubmissionStatusNew, at.Add(-time.Minute), map[string]any{"name": "Other", "email": "b@example.com"})
	ff.addSub(fm, form.SubmissionStatusRead, at.Add(-2*time.Minute), map[string]any{"name": "Match read"})
	export := func(query string) [][]string {
		t.Helper()
		rec := g.do("GET", "/admin/forms/submissions/export.csv?form="+fm.ID.String()+query, "owner-token", nil, nil)
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf"))).ReadAll()
		if rec.Code != 200 || err != nil {
			t.Fatalf("export %q: %d %v", query, rec.Code, err)
		}
		return rows
	}
	if rows := export(""); len(rows) != 4 {
		t.Errorf("without a filter the export holds %d rows, want header + 3", len(rows))
	}
	rows := export("&status=new&q=match")
	if len(rows) != 2 || !strings.HasPrefix(rows[1][3], "'=") {
		t.Fatalf("status=new&q=match: %v (want the one unread match, formula neutralised)", rows)
	}
	if rows := export("&q=" + url.QueryEscape("  MATCH ")); len(rows) != 3 {
		t.Errorf("a trimmed, case-insensitive search found %d rows, want header + 2", len(rows)-1)
	}
}

func TestSubmissionActionsReturnToTabWithFilters(t *testing.T) {
	t.Parallel()

	g, ff, _, fm, s := submissionRig(t)
	s2 := ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Bob"})
	tab := "/admin/forms/design/" + fm.ID.String() + "/submissions"
	back := tab + "?q=bob&status=new&page=2&s=" + s.ID.String()
	want := tab + "?page=2&q=bob&status=new"
	bulk := "/admin/forms/submissions/bulk?form=" + fm.ID.String()

	rec := g.do("POST", bulk, "owner-token", url.Values{"action": {"read"}, "id": {s.ID.String()}, "back": {back}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != want || ff.subs[s.ID].Status != form.SubmissionStatusRead {
		t.Fatalf("bulk read: %d -> %q, status %s", rec.Code, rec.Header().Get("Location"), ff.subs[s.ID].Status)
	}
	rec = g.do("POST", bulk, "owner-token", url.Values{"action": {"spam"}, "id": {s2.ID.String()}, "back": {back}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != want || ff.subs[s2.ID].Status != form.SubmissionStatusSpam {
		t.Fatalf("bulk spam: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	// Single status keeps the filters too.
	rec = g.do("POST", "/admin/forms/submissions/"+s.ID.String()+"/status", "owner-token", url.Values{"status": {"new"}, "back": {back}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != want || ff.subs[s.ID].Status != form.SubmissionStatusNew {
		t.Fatalf("status: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	// Bulk delete asks first (the page keeps the filters), then returns to the tab.
	rec = g.do("POST", bulk, "owner-token", url.Values{"action": {"delete"}, "id": {s.ID.String()}, "back": {back}}, nil)
	if rec.Code != 200 || len(ff.subs) != 2 || !strings.Contains(rec.Body.String(), "back="+url.QueryEscape(want)) {
		t.Fatalf("bulk delete must ask first: %d subs=%d", rec.Code, len(ff.subs))
	}
	rec = g.do("POST", bulk, "owner-token", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {s.ID.String()}, "back": {back}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != want || len(ff.subs) != 1 {
		t.Fatalf("confirmed bulk delete: %d -> %q subs=%d", rec.Code, rec.Header().Get("Location"), len(ff.subs))
	}
	// Single delete: GET asks and never deletes; POST deletes and returns.
	del := "/admin/forms/submissions/" + s2.ID.String() + "/delete"
	rec = g.do("GET", del+"?back="+url.QueryEscape(back), "owner-token", nil, nil)
	if rec.Code != 200 || len(ff.subs) != 1 || !strings.Contains(rec.Body.String(), `href="`+html.EscapeString(want)+`"`) {
		t.Fatalf("delete confirm: %d subs=%d", rec.Code, len(ff.subs))
	}
	rec = g.do("POST", del+"?back="+url.QueryEscape(back), "owner-token", url.Values{}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != want || len(ff.subs) != 0 {
		t.Fatalf("delete: %d -> %q subs=%d", rec.Code, rec.Header().Get("Location"), len(ff.subs))
	}
}

func TestSubmissionBackRejectsOtherAddresses(t *testing.T) {
	t.Parallel()

	g, ff, _, fm, _ := submissionRig(t)
	plain := "/admin/forms/design/" + fm.ID.String() + "/submissions"
	bulk := "/admin/forms/submissions/bulk?form=" + fm.ID.String()
	other := ff.addForm("Other")
	for _, back := range []string{"//evil.example/x", "https://evil.example/admin/forms", "/elsewhere", `/admin\\evil`, "javascript:alert(1)", "", "/admin/forms/design/" + other.ID.String() + "/submissions?q=x&status=bogus"} {
		rec := g.do("POST", bulk, "owner-token", url.Values{"action": {"read"}, "back": {back}}, nil)
		loc := rec.Header().Get("Location")
		if rec.Code != 303 || !strings.HasPrefix(loc, plain) || strings.Contains(loc, "evil") || strings.Contains(loc, "status=bogus") {
			t.Errorf("back %q: %d -> %q, want the form's own tab", back, rec.Code, loc)
		}
	}
}

// Every action the drawer offers is a form or link in the drawer itself: this
// posts what the opened drawer renders and checks each result.
func TestSubmissionDrawerActionsWork(t *testing.T) {
	t.Parallel()

	g, ff, mail, fm, s := submissionRig(t)
	tab := "/admin/forms/design/" + fm.ID.String() + "/submissions"
	filters := "?q=ada&status=read"
	wantBack := tab + "?q=ada&status=read"
	open := func() string {
		rec := g.do("GET", tab+filters+"&s="+s.ID.String(), "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "hm-ss-drawer"})
		if rec.Code != 200 {
			t.Fatalf("drawer: %d", rec.Code)
		}
		return rec.Body.String()
	}
	form0 := func(drawer, action string) url.Values {
		t.Helper()
		i := strings.Index(drawer, `action="`+action+`"`)
		if i < 0 {
			t.Fatalf("drawer has no form posting to %s:\n%s", action, drawer)
		}
		end := i + strings.Index(drawer[i:], "</form>")
		vals := url.Values{}
		for _, m := range regexp.MustCompile(`<input[^>]*type="hidden"[^>]*name="([^"]+)"[^>]*value="([^"]*)"`).FindAllStringSubmatch(drawer[i:end], -1) {
			vals.Set(m[1], html.UnescapeString(m[2]))
		}
		return vals
	}
	st := "/admin/forms/submissions/" + s.ID.String() + "/status"
	drawer := open() // opening marks it read
	if !strings.Contains(drawer, `href="mailto:ada@example.com"`) || !strings.Contains(drawer, "Mark unread") || !strings.Contains(drawer, "Archive") || !strings.Contains(drawer, "Forward by email") {
		t.Fatalf("drawer lacks Reply, Mark unread, Archive or Forward by email:\n%s", drawer)
	}
	if !strings.Contains(drawer, "/delete?back=") {
		t.Error("drawer lacks Delete")
	}
	for _, c := range []struct{ status, want string }{{"new", form.SubmissionStatusNew}, {"archived", form.SubmissionStatusArchived}, {"spam", form.SubmissionStatusSpam}} {
		ff.subs[s.ID].Status = form.SubmissionStatusRead
		vals := form0(drawer, st)
		// The first status form is spam; pick the form that sets c.status.
		for _, m := range regexp.MustCompile(`(?s)<form method="post" action="`+regexp.QuoteMeta(st)+`">.*?</form>`).FindAllString(drawer, -1) {
			if strings.Contains(m, `name="status" value="`+c.status+`"`) {
				vals = form0(m, st)
			}
		}
		if vals.Get("status") != c.status {
			t.Fatalf("drawer has no %s button (got %q)", c.status, vals.Get("status"))
		}
		rec := g.do("POST", st, "owner-token", vals, nil)
		if rec.Code != 303 || rec.Header().Get("Location") != wantBack || ff.subs[s.ID].Status != c.want {
			t.Errorf("%s: %d -> %q, status %s", c.status, rec.Code, rec.Header().Get("Location"), ff.subs[s.ID].Status)
		}
	}
	ff.subs[s.ID].Status = form.SubmissionStatusRead
	vals := form0(drawer, "/admin/forms/submissions/"+s.ID.String()+"/email")
	vals.Set("to", "sales@example.com")
	rec := g.do("POST", "/admin/forms/submissions/"+s.ID.String()+"/email", "owner-token", vals, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != wantBack+"&s="+s.ID.String() || len(mail.sent) != 1 {
		t.Errorf("forward: %d -> %q sent %d", rec.Code, rec.Header().Get("Location"), len(mail.sent))
	}
	mail.on = false
	if strings.Contains(open(), "Forward by email") {
		t.Error("the drawer offers forwarding although email is not set up")
	}
}
