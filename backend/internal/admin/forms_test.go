package admin

import (
	"context"
	"encoding/csv"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/spam"
)

type fakeForms struct {
	mu    sync.Mutex
	forms map[uuid.UUID]*form.Form
	subs  map[uuid.UUID]*form.FormSubmission
	// calls counts ListSubmissions pages, to prove the export pages through.
	listCalls int
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
	fm.Description, fm.Settings = in.Description, in.Settings
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
func (f *fakeForms) DeleteForm(_ context.Context, id uuid.UUID) error {
	if _, ok := f.forms[id]; !ok {
		return form.ErrFormNotFound
	}
	delete(f.forms, id)
	return nil
}
func (f *fakeForms) ListSubmissions(_ context.Context, formID uuid.UUID, p *form.ListSubmissionsParams) ([]*form.FormSubmission, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	var all []*form.FormSubmission
	for _, s := range f.subs {
		if s.FormID == formID && (p == nil || p.Status == "" || s.Status == p.Status) {
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
	body := g.do("GET", "/admin/settings/forms", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "Contact") || !strings.Contains(body, `href="/admin/settings/forms/new"`) {
		t.Fatalf("list: %s", body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("list a11y: %v", issues)
	}
	if !strings.Contains(body, `hm-badge--info`) || !strings.Contains(body, "Last 14 days") {
		t.Errorf("list lacks the New badge or the 14-day column")
	}
	newPage := g.do("GET", "/admin/settings/forms/new", "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(newPage, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("new a11y: %v", issues)
	}
	if rec := g.do("POST", "/admin/settings/forms", "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a name") {
		t.Errorf("blank name: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/settings/forms", "owner-token", url.Values{"name": {"Newsletter"}}, nil)
	if rec.Code != 303 || !strings.HasSuffix(rec.Header().Get("Location"), "/edit") {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if g.do("GET", "/admin/settings/forms/"+fm.ID.String()+"/delete", "owner-token", nil, nil).Code != 200 || len(ff.forms) != 2 {
		t.Fatal("the confirmation page must not delete")
	}
	if rec := g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 || len(ff.forms) != 1 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func fieldForm(rows ...map[string]string) url.Values {
	v := url.Values{"name": {"Contact"}, "status": {"active"}, "_op": {"save"}, "store": {"1"}, "submit_text": {"Send"}}
	for i, r := range rows {
		for k, val := range r {
			v.Set("f."+strconv.Itoa(i)+"."+k, val)
		}
	}
	return v
}

func TestFormEditUsesPublicAPIPrefix(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ prefix, want, gone string }{
		{"", "/api/v1/public/forms/contact/submit", "/hook/"},
		{"/hook", "/hook/forms/contact/submit", "/api/v1/public/forms"},
	} {
		g, ff, _ := newFormRig(t, func(d *Deps) { d.PublicAPIPrefix = tc.prefix })
		fm := ff.addForm("Contact", contactFields...)
		body := g.do("GET", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", nil, nil).Body.String()
		// the endpoint field, the copy button and the embed code
		if n := strings.Count(body, tc.want); n < 3 {
			t.Errorf("prefix %q: %q appears %d times, want at least 3", tc.prefix, tc.want, n)
		}
		if strings.Contains(body, tc.gone) {
			t.Errorf("prefix %q: page still names %q", tc.prefix, tc.gone)
		}
	}
}

func TestFormBuilderSaveAndValidation(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	edit := g.do("GET", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", nil, nil)
	if edit.Code != 200 {
		t.Fatal(edit.Code)
	}
	if issues := a11y.Check(edit.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("edit a11y: %v", issues)
	}
	f := fieldForm(
		map[string]string{"id": "a", "type": "text", "label": "Full name", "required": "1", "maxlen": "80"},
		map[string]string{"type": "select", "label": "Topic", "options": "Sales\nSupport | help\n"},
		map[string]string{"type": "email", "label": "Email", "name": "email", "pattern": `^[^@]+@[^@]+$`},
	)
	f.Set("notify_email", "a@example.com, b@example.com")
	f.Set("captcha", "1")
	f.Set("redirect_url", "/thanks")
	rec := g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", f, nil)
	if rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got := ff.forms[fm.ID]
	if len(got.Fields) != 3 || got.Fields[0].ID != "a" || got.Fields[0].Name != "full_name" || got.Fields[1].ID == "" || got.Fields[1].Name != "topic" {
		t.Fatalf("fields: %+v", got.Fields)
	}
	if o := got.Fields[1].Options; len(o) != 2 || o[0].Value != "sales" || o[1].Value != "help" {
		t.Errorf("options: %+v", o)
	}
	if v := got.Fields[0].Validation; v == nil || *v.MaxLength != 80 {
		t.Errorf("validation: %+v", got.Fields[0].Validation)
	}
	if !got.Settings.EnableCaptcha || got.Settings.SuccessRedirectURL != "/thanks" || *got.Settings.NotifyEmail != "a@example.com, b@example.com" || got.Settings.SubmitButtonText != "Send" {
		t.Errorf("settings: %+v", got.Settings)
	}

	for name, bad := range map[string]url.Values{
		"duplicate name": fieldForm(map[string]string{"type": "text", "label": "A", "name": "x"}, map[string]string{"type": "text", "label": "B", "name": "x"}),
		"bad pattern":    fieldForm(map[string]string{"type": "text", "label": "A", "pattern": "("}),
		"no options":     fieldForm(map[string]string{"type": "radio", "label": "A"}),
		"blank label":    fieldForm(map[string]string{"type": "text", "label": ""}),
		"bad field name": fieldForm(map[string]string{"type": "text", "label": "A", "name": "Bad Name"}),
		"bad number":     fieldForm(map[string]string{"type": "text", "label": "A", "minlen": "x"}),
	} {
		before := len(ff.forms[fm.ID].Fields)
		rec := g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", bad, nil)
		if rec.Code != 422 || len(ff.forms[fm.ID].Fields) != before {
			t.Errorf("%s: want 422 and no save, got %d", name, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", name, issues)
		}
	}
	for name, mutate := range map[string]func(url.Values){
		"bad notify":   func(v url.Values) { v.Set("notify_email", "not-an-email") },
		"javascript:":  func(v url.Values) { v.Set("redirect_url", "javascript:alert(1)") },
		"protocol-rel": func(v url.Values) { v.Set("redirect_url", "//evil.example") },
	} {
		v := fieldForm()
		mutate(v)
		if rec := g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", v, nil); rec.Code != 422 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestFormBuilderOpsWithoutJS(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact")
	base := func() url.Values {
		return fieldForm(map[string]string{"type": "text", "label": "One"}, map[string]string{"type": "text", "label": "Two"})
	}
	pos := func(body, label string) int { return strings.Index(body, `value="`+label+`"`) }
	f := base()
	f.Set("_op", "down.0")
	body := g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", f, nil).Body.String()
	if pos(body, "Two") > pos(body, "One") || !strings.Contains(body, "not saved yet") || len(ff.forms[fm.ID].Fields) != 0 {
		t.Error("down should swap without saving")
	}
	f = base()
	f.Set("_op", "remove.0")
	body = g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", f, nil).Body.String()
	if pos(body, "One") >= 0 || pos(body, "Two") < 0 {
		t.Error("remove should drop the first field")
	}
	f = base()
	f.Set("_op", "add")
	f.Set("add_type", "select")
	body = g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", f, nil).Body.String()
	if !strings.Contains(body, `name="f.2.label"`) {
		t.Error("add should append a third row")
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

func TestSubmissionListDetailAndStatus(t *testing.T) {
	t.Parallel()

	g, ff, _, fm, s := submissionRig(t)
	base := "/admin/settings/submissions"
	listURL := base + "?form=" + fm.ID.String()
	list := g.do("GET", listURL, "owner-token", nil, nil).Body.String()
	if !strings.Contains(list, "Name: Ada") || !strings.Contains(list, "Export CSV") || strings.Contains(list, "<b>Lovelace") {
		t.Fatalf("list: %s", list)
	}
	if issues := a11y.Check(list, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("list a11y: %v", issues)
	}
	if body := g.do("GET", listURL+"&status=spam", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "No submissions") {
		t.Error("status filter should hide the new submission")
	}
	rec := g.do("GET", base+"/"+s.ID.String(), "owner-token", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{"Ada &lt;b&gt;Lovelace", "legacy", "kept", "https://example.com/contact"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	if ff.subs[s.ID].Status != form.SubmissionStatusRead {
		t.Error("opening a new submission marks it read")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("detail a11y: %v", issues)
	}
	g.do("POST", base+"/"+s.ID.String()+"/status", "owner-token", url.Values{"status": {"spam"}}, nil)
	if ff.subs[s.ID].Status != form.SubmissionStatusSpam {
		t.Error("status not changed")
	}
	if rec := g.do("POST", base+"/"+s.ID.String()+"/status", "owner-token", url.Values{"status": {"bogus"}}, nil); rec.Code != 400 {
		t.Errorf("bad status: %d", rec.Code)
	}
	// An unknown submission is a 404, and the list of an unknown form too.
	if rec := g.do("GET", base+"/"+uuid.NewString(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown submission: %d", rec.Code)
	}
	if rec := g.do("GET", base+"?form="+uuid.NewString(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown form: %d", rec.Code)
	}
	// With no ?form= the first form is listed, and the picker names the others.
	if body := g.do("GET", base, "owner-token", nil, nil).Body.String(); !strings.Contains(body, `<select class="hm-field__control" id="sub-form"`) {
		t.Error("submissions page has no form picker")
	}
}

func TestSubmissionEmailForward(t *testing.T) {
	t.Parallel()

	g, _, mail, _, s := submissionRig(t)
	url0 := "/admin/settings/submissions/" + s.ID.String() + "/email"
	if rec := g.do("POST", url0, "owner-token", url.Values{"to": {"nope"}}, nil); rec.Code != 422 || len(mail.sent) != 0 {
		t.Fatalf("invalid recipient: %d", rec.Code)
	}
	if rec := g.do("POST", url0, "owner-token", url.Values{"to": {"a@example.com, b@example.com"}}, nil); rec.Code != 303 || len(mail.sent) != 1 || len(mail.sent[0]) != 2 {
		t.Fatalf("send: %d %v", rec.Code, mail.sent)
	}
	mail.err = errors.New("smtp down")
	if rec := g.do("POST", url0, "owner-token", url.Values{"to": {"a@example.com"}}, nil); rec.Code != 502 {
		t.Errorf("mail failure: %d", rec.Code)
	}
	mail.on = false
	g.do("POST", url0, "owner-token", url.Values{"to": {"a@example.com"}}, nil)
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
	base := "/admin/settings/submissions"
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
	rec := g.do("GET", "/admin/settings/submissions/export.csv?form="+fm.ID.String()+"&status=read", "owner-token", nil, nil)
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

func TestSubmissionDetailShowsWhereItCameFrom(t *testing.T) {
	t.Parallel()

	g, _, _, _, s := submissionRig(t)
	url := "/admin/settings/submissions/" + s.ID.String()
	body := g.do("GET", url, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "sub-came-from") || !strings.Contains(body, "<dt>First touch</dt><dd>Unknown</dd>") || !strings.Contains(body, "<dt>Last touch</dt><dd>Unknown</dd>") {
		t.Errorf("a submission without attribution reads Unknown on both touches: %s", body)
	}
	s.Attribution = &form.Attribution{
		First: &form.Touch{Channel: "ai_assistants", Source: "chatgpt", Landing: "/post/a"},
		Last:  &form.Touch{Channel: "email", Source: "newsletter", Campaign: "oct-news", Landing: "/pricing"}}
	body = g.do("GET", url, "owner-token", nil, nil).Body.String()
	for _, want := range []string{"<dt>First touch</dt><dd>AI assistants / chatgpt / /post/a</dd>", "<dt>Last touch</dt><dd>Email / newsletter / oct-news / /pricing</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
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
	rec := g.do("GET", "/admin/settings/submissions/export.csv?form="+fm.ID.String(), "owner-token", nil, nil)
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
	u := "/admin/settings/submissions/" + s.ID.String() + "/delete"
	rec := g.do("GET", u, "owner-token", nil, nil)
	if rec.Code != 200 || len(ff.subs) != 1 {
		t.Fatalf("confirm page: %d", rec.Code)
	}
	if rec := g.do("POST", u, "owner-token", url.Values{}, nil); rec.Code != 303 || len(ff.subs) != 0 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestCarryFieldDataKeepsAPIOnlyData(t *testing.T) {
	t.Parallel()

	min, max := 1, 9
	stored := []form.FormField{{ID: "a", DefaultValue: "x", Attributes: map[string]string{"k": "v"}, Validation: &form.FieldValidation{Min: &min, Max: &max}}}
	got := carryFieldData([]form.FormField{{ID: "a", Validation: &form.FieldValidation{MaxLength: &max}}, {ID: "new"}}, stored)
	v := got[0].Validation
	if got[0].DefaultValue != "x" || got[0].Attributes["k"] != "v" || *v.Min != 1 || *v.Max != 9 || *v.MaxLength != 9 {
		t.Errorf("not carried: %+v %+v", got[0], v)
	}
	if got[1].Validation != nil || got[1].DefaultValue != "" {
		t.Errorf("new field must stay empty: %+v", got[1])
	}
}

func TestSubmissionPrefetchDoesNotMarkRead(t *testing.T) {
	t.Parallel()

	g, _, _, _, s := submissionRig(t)
	u := "/admin/settings/submissions/" + s.ID.String()
	if rec := g.do("GET", u, "owner-token", nil, map[string]string{"Sec-Purpose": "prefetch;prerender"}); rec.Code != 200 {
		t.Fatalf("prerender GET: %d", rec.Code)
	}
	if s.Status != form.SubmissionStatusNew {
		t.Fatalf("a prerender marked the submission %q", s.Status)
	}
	g.do("GET", u, "owner-token", nil, nil)
	if s.Status != form.SubmissionStatusRead {
		t.Errorf("a normal open left the submission %q", s.Status)
	}
}

func TestFormEditCaptchaBanner(t *testing.T) {
	const warn = "Captcha is on for this form but no provider keys are set. Submissions are accepted unchecked."
	g, ff, _ := newFormRig(t)
	on := ff.addForm("Contact", contactFields...)
	on.Settings.EnableCaptcha = true
	off := ff.addForm("Plain", contactFields...)
	page := func(fm *form.Form, token string) string {
		rec := g.do("GET", "/admin/settings/forms/"+fm.ID.String()+"/edit", token, nil, nil)
		if rec.Code != 200 {
			t.Fatalf("edit = %d", rec.Code)
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
	if !strings.Contains(owner, warn) || !strings.Contains(owner, `href="/admin/settings/spam"`) {
		t.Error("owner lacks the banner or its link with no keys")
	}
	editor := page(on, "editor-token")
	if !strings.Contains(editor, warn) || strings.Contains(editor, "/admin/settings/spam") {
		t.Error("editor should see the banner without a link")
	}
	if strings.Contains(page(off, "owner-token"), warn) {
		t.Error("banner on a form with the switch off")
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

// Builder rows are <details>: a new blank row and a row with an error render
// open, a saved one closed, and Choices is shown by data-type, not by script.
func TestFormBuilderRowsCollapseAndOpenOnError(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact")
	f := fieldForm(map[string]string{"type": "text", "label": "One", "id": "keep"}, map[string]string{"type": "select", "label": "", "id": "keep2"})
	f.Set("_op", "save")
	rec := g.do("POST", "/admin/settings/forms/"+fm.ID.String()+"/edit", "owner-token", f, nil)
	body := rec.Body.String()
	if rec.Code != 422 || strings.Count(body, `<details class="hm-fieldlist__row" open>`) != 1 || strings.Count(body, `<details class="hm-fieldlist__row">`) != 1 {
		t.Errorf("want one open (errored) and one closed row, status %d", rec.Code)
	}
	if !strings.Contains(body, `data-type="select"`) || !strings.Contains(body, `hm-fieldlist__choices`) {
		t.Error("choices wrapper or data-type missing")
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
	body := g.do("GET", "/admin/settings/submissions?form="+fm.ID.String(), "owner-token", nil, nil).Body.String()
	for _, want := range []string{"hm-tracker", ">Total<", ">New<", ">Last 7 days<", "data-hm-autosubmit", "<noscript>", `: 1 submission, busiest"`} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if strings.Contains(body, "5 submissions") {
		t.Error("spam counted in the tracker")
	}
}

func TestFormActivityCells(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	subs := []*form.FormSubmission{{CreatedAt: now.Add(-time.Hour)}, {CreatedAt: now.Add(-48 * time.Hour)}}
	total, last := formActivityCells(subs, 9, false, nil, now, time.UTC)
	if total.Text != "9" || last.Text != "" || last.Spark == nil {
		t.Errorf("complete scan: total %+v last %+v", total, last)
	}
	total, last = formActivityCells(subs, 9, true, nil, now, time.UTC)
	if total.Text != "9" || last.Text != "2+" || last.Spark != nil {
		t.Errorf("capped scan must show a lower bound and no spark: total %+v last %+v", total, last)
	}
	total, last = formActivityCells(nil, 0, false, errors.New("db down"), now, time.UTC)
	if total.Text != "—" || last.Text != "—" {
		t.Errorf("failed scan must show dashes, not 0: total %+v last %+v", total, last)
	}
}
