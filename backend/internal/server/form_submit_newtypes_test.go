package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/email/smtptest"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/jsonx"
)

func newTypesServer(t *testing.T, f *form.Form) (*Server, *fakeFormRepo) {
	t.Helper()
	f.ID, f.Slug, f.Status = uuid.New(), "t", form.StatusActive
	f.Settings.StoreSubmissions = true
	repo := &fakeFormRepo{forms: map[string]*form.Form{"t": f}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{cfg: &config.Config{}, logger: logger, formService: form.NewService(repo, logger)}, repo
}

// submitJSON posts to the JSON endpoint and decodes the reply.
func submitJSON(t *testing.T, srv *Server, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/forms/t/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Page-URL", "https://site.test/p?utm=spring")
	req.SetPathValue("slug", "t")
	rec := httptest.NewRecorder()
	srv.handlePublicFormSubmit(rec, req)
	var out map[string]any
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("reply %q: %v", rec.Body.String(), err)
	}
	return rec.Code, out
}

func TestFormSubmitNewTypes(t *testing.T) {
	t.Parallel()
	fields := []form.FormField{
		{ID: "h", Type: form.FieldTypeHeading, Text: "About you"},
		{ID: "p", Type: form.FieldTypeParagraph, Text: "Intro"},
		{ID: "d", Type: form.FieldTypeDivider},
		{ID: "n", Type: form.FieldTypeText, Name: "name", Label: "Name", Required: true},
		{ID: "k", Type: form.FieldTypeHidden, Name: "campaign", Label: "Campaign", Hidden: &form.HiddenSpec{Source: "static", Value: "spring"}},
		{ID: "c", Type: form.FieldTypeConsent, Name: "agree", Label: "Terms"},
		{ID: "e", Type: form.FieldTypeEmail, Name: "email", Label: "Email", BlockFreeEmail: true},
		{ID: "x", Type: form.FieldTypeText, Name: "extra", Label: "Extra", Required: true,
			Logic: &form.FieldLogic{FieldID: "n", Op: "is", Value: "Grace"}},
	}
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantData   map[string]any
		wantFields map[string]string
	}{
		{"ok, posted hidden ignored, consent stored Yes, logic-hidden dropped",
			`{"name":"Ada","campaign":"forged","agree":"on","extra":"zz","heading":"x"}`, 201,
			map[string]any{"name": "Ada", "campaign": "spring", "agree": "Yes"}, nil},
		{"consent missing", `{"name":"Ada"}`, 400, nil, map[string]string{"agree": "Terms is required"}},
		{"logic-shown field is required", `{"name":"Grace","agree":true}`, 400, nil, map[string]string{"extra": "Extra is required"}},
		{"free email blocked", `{"name":"Ada","agree":"yes","email":"a@aol.com"}`, 400, nil, map[string]string{"email": "Email must be a work email address"}},
		{"work email allowed", `{"name":"Ada","agree":"yes","email":"a@corp.example"}`, 201,
			map[string]any{"name": "Ada", "campaign": "spring", "agree": "Yes", "email": "a@corp.example"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			srv, repo := newTypesServer(t, &form.Form{Fields: fields})
			code, out := submitJSON(t, srv, c.body)
			if code != c.wantStatus {
				t.Fatalf("status %d, want %d: %v", code, c.wantStatus, out)
			}
			if c.wantFields != nil {
				got, _ := out["fields"].(map[string]any)
				if len(got) != len(c.wantFields) {
					t.Fatalf("fields = %v, want %v", got, c.wantFields)
				}
				for k, v := range c.wantFields {
					if got[k] != v {
						t.Errorf("fields[%s] = %v, want %q", k, got[k], v)
					}
				}
				if len(repo.subs) != 0 {
					t.Error("nothing should be stored")
				}
				return
			}
			if len(repo.subs) != 1 {
				t.Fatalf("submissions = %d", len(repo.subs))
			}
			data := repo.subs[0].Data
			if len(data) != len(c.wantData) {
				t.Fatalf("data = %v, want %v", data, c.wantData)
			}
			for k, v := range c.wantData {
				if data[k] != v {
					t.Errorf("data[%s] = %v, want %v", k, data[k], v)
				}
			}
		})
	}
}

func TestFormSubmitHiddenFromPageParam(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, &form.Form{Fields: []form.FormField{
		{ID: "k", Type: form.FieldTypeHidden, Name: "utm", Label: "UTM", Hidden: &form.HiddenSpec{Source: "param", Param: "utm"}},
	}})
	if code, out := submitJSON(t, srv, `{"utm":"forged"}`); code != 201 {
		t.Fatalf("%d %v", code, out)
	}
	if got := repo.subs[0].Data["utm"]; got != "spring" {
		t.Errorf("utm = %v, want spring", got)
	}
}

func TestFormSubmitNewTypesNativePost(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, &form.Form{Fields: []form.FormField{
		{ID: "h", Type: form.FieldTypeHeading, Text: "Hi"},
		{ID: "c", Type: form.FieldTypeConsent, Name: "agree", Label: "Terms"},
	}})
	rec := postForm(srv, "t", url.Values{"agree": {"on"}, "_redirect": {"/c"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/c?submitted=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(repo.subs) != 1 || repo.subs[0].Data["agree"] != "Yes" || len(repo.subs[0].Data) != 1 {
		t.Errorf("data = %v", repo.subs)
	}
	rec = postForm(srv, "t", url.Values{"_redirect": {"/c"}})
	if rec.Header().Get("Location") == "/c?submitted=1" || len(repo.subs) != 1 {
		t.Errorf("missing consent must not store: %q subs=%d", rec.Header().Get("Location"), len(repo.subs))
	}
}

func TestFormSubmitEmailSkipsInEmailFalseAndLayout(t *testing.T) {
	t.Setenv("EMAIL_DISABLED", "")
	sink := smtptest.Start(t, smtptest.Options{Mode: smtptest.Plain})
	svc, err := email.NewService(email.Config{
		SMTPHost: sink.Host(), SMTPPort: sink.Port(), FromAddress: "site@example.test", Security: email.SecurityNone,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	off := false
	notify := "ops@y.test"
	srv, _ := newTypesServer(t, &form.Form{
		Name:     "Contact",
		Settings: form.FormSettings{NotifyEmail: &notify},
		Fields: []form.FormField{
			{ID: "h", Type: form.FieldTypeHeading, Label: "LayoutHeadingLabel", Text: "x"},
			{ID: "a", Type: form.FieldTypeText, Name: "shown", Label: "ShownLabel"},
			{ID: "b", Type: form.FieldTypeText, Name: "private", Label: "PrivateLabel", InEmail: &off},
		},
	})
	srv.emailService = svc
	srv.adminBundle.Store(&adminBundle{prefix: "/hq"})
	if code, out := submitJSON(t, srv, `{"shown":"v1","private":"v2"}`); code != 201 {
		t.Fatalf("%d %v", code, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(sink.Messages()) < 1 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	msgs := sink.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	body := msgs[0].Data
	if !strings.Contains(body, "ShownLabel") {
		t.Errorf("default field missing from email:\n%s", body)
	}
	for _, bad := range []string{"PrivateLabel", "v2", "LayoutHeadingLabel"} {
		if strings.Contains(body, bad) {
			t.Errorf("email contains %q:\n%s", bad, body)
		}
	}
}

func hiddenSourcesForm() *form.Form {
	return &form.Form{Fields: []form.FormField{
		{ID: "u", Type: form.FieldTypeHidden, Name: "utm", Label: "UTM", Hidden: &form.HiddenSpec{Source: "param", Param: "utm"}},
		{ID: "r", Type: form.FieldTypeHidden, Name: "ref", Label: "Ref", Hidden: &form.HiddenSpec{Source: "referrer"}},
		{ID: "p", Type: form.FieldTypeHidden, Name: "where", Label: "Page", Hidden: &form.HiddenSpec{Source: "page"}},
	}}
}

func TestFormPostHiddenSourcesUseThePageScriptFields(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, hiddenSourcesForm())
	// postForm sends Referer = the form's own page; the visitor's referrer is hm_ref.
	rec := postForm(srv, "t", url.Values{
		"_redirect": {"/c"}, "page_url": {"/c"},
		"hm_page": {"https://site.test/c?utm=spring"}, "hm_ref": {"https://search.test/q"},
	})
	if rec.Code != http.StatusSeeOther || len(repo.subs) != 1 {
		t.Fatalf("%d subs=%d", rec.Code, len(repo.subs))
	}
	want := map[string]any{"utm": "spring", "ref": "https://search.test/q", "where": "https://site.test/c?utm=spring"}
	for k, v := range want {
		if got := repo.subs[0].Data[k]; got != v {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
	}
	if _, ok := repo.subs[0].Data["hm_page"]; ok {
		t.Error("hm_page was stored as an answer")
	}
}

func TestFormPostHiddenReferrerIsNotThePageItself(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, hiddenSourcesForm())
	postForm(srv, "t", url.Values{"_redirect": {"/c"}, "page_url": {"/c"}})
	if got, ok := repo.subs[0].Data["ref"]; ok {
		t.Errorf("without the page script the referrer stays empty, got %v (the submit Referer is the page)", got)
	}
}

func TestFormSubmitJSONHiddenSourcesUseReservedFields(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, hiddenSourcesForm())
	body := `{"hm_page":"https://site.test/c?utm=fall","hm_ref":"https://news.test/"}`
	if code, out := submitJSON(t, srv, body); code != 201 {
		t.Fatalf("%d %v", code, out)
	}
	d := repo.subs[0].Data
	if d["utm"] != "fall" || d["ref"] != "https://news.test/" || len(d) != 3 {
		t.Errorf("data = %v", d)
	}
}

func TestFormHiddenPageAndReferrerAreRedactedLikeTheEmail(t *testing.T) {
	t.Parallel()
	srv, repo := newTypesServer(t, hiddenSourcesForm())
	srv.adminBundle.Store(&adminBundle{prefix: "/hq-x7k"})
	postForm(srv, "t", url.Values{"_redirect": {"/c"},
		"hm_page": {"https://site.test/hq-x7k/entities/1/edit?utm=x"}, "hm_ref": {"https://site.test/hq-x7k/forms"}})
	for k, v := range repo.subs[0].Data {
		if strings.Contains(fmt.Sprint(v), "hq-x7k") {
			t.Errorf("%s stored the admin address: %v", k, v)
		}
	}
}
