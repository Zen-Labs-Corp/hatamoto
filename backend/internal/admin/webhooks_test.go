package admin

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/webhook"
)

// fakeWebhooks is an in-memory WebhookStore.
type fakeWebhooks struct {
	subs    []webhook.ListedSubscription
	created []webhook.Subscription
	updated map[string]webhook.SubscriptionPatch
	// test is what SendTest answers; tested counts the calls.
	test       webhook.DeliveryRow
	deliveries []webhook.DeliveryRow
	retried    []string
	gotSub     string
	gotLimit   int
	gotOffset  int
	tested     int
	regen      int
	// failedSince is what CountFailedSince answers; failedErr fails it.
	failedSince int
	failedErr   error
	failedAsked time.Time
}

func (f *fakeWebhooks) CountFailedSince(_ context.Context, since time.Time) (int, error) {
	f.failedAsked = since
	return f.failedSince, f.failedErr
}

func (f *fakeWebhooks) ListDeliveries(_ context.Context, sub string, limit, offset int) ([]webhook.DeliveryRow, int, error) {
	f.gotSub, f.gotLimit, f.gotOffset = sub, limit, offset
	return f.deliveries, len(f.deliveries), nil
}

func (f *fakeWebhooks) RetryDelivery(_ context.Context, id string) (bool, error) {
	for i, d := range f.deliveries {
		if d.ID == id {
			if d.Status != "failed" {
				return false, nil
			}
			f.deliveries[i].Status = "pending"
			f.retried = append(f.retried, id)
			return true, nil
		}
	}
	return false, webhook.ErrNotFound
}

func (f *fakeWebhooks) find(id string) int {
	for i, s := range f.subs {
		if s.ID == id {
			return i
		}
	}
	return -1
}

func (f *fakeWebhooks) SetActive(_ context.Context, id string, active bool) error {
	i := f.find(id)
	if i < 0 {
		return webhook.ErrNotFound
	}
	f.subs[i].Active = active
	return nil
}

func (f *fakeWebhooks) RegenerateSecret(_ context.Context, id string) (string, error) {
	if f.find(id) < 0 {
		return "", webhook.ErrNotFound
	}
	f.regen++
	return "whsec_REGENERATED", nil
}

func (f *fakeWebhooks) Delete(_ context.Context, id string) error {
	i := f.find(id)
	if i < 0 {
		return webhook.ErrNotFound
	}
	f.subs = append(f.subs[:i], f.subs[i+1:]...)
	return nil
}

func (f *fakeWebhooks) SendTest(_ context.Context, id string) (*webhook.DeliveryRow, error) {
	if f.find(id) < 0 {
		return nil, webhook.ErrNotFound
	}
	f.tested++
	row := f.test
	return &row, nil
}

const fakeSecret = "whsec_THE-ONLY-SECRET"

func (f *fakeWebhooks) Create(_ context.Context, in webhook.Subscription) (webhook.Subscription, string, error) {
	if err := in.Validate(); err != nil {
		return webhook.Subscription{}, "", err
	}
	in.ID = "33333333-3333-4333-8333-333333333333"
	f.created = append(f.created, in)
	f.subs = append(f.subs, webhook.ListedSubscription{ID: in.ID, Name: in.Name, URL: in.URL, Events: in.Events, Active: in.Active})
	return in, fakeSecret, nil
}

func (f *fakeWebhooks) Get(_ context.Context, id string) (*webhook.Subscription, error) {
	for _, s := range f.subs {
		if s.ID == id {
			return &webhook.Subscription{ID: s.ID, Name: s.Name, URL: s.URL, Secret: fakeSecret, Events: s.Events, Headers: map[string]string{"X-Team": "ops"}, Active: s.Active,
				MaxRetries: 3, RetryDelaySeconds: 60, TimeoutSeconds: 5, AllowHTTP: s.AllowHTTP}, nil
		}
	}
	return nil, webhook.ErrNotFound
}

func (f *fakeWebhooks) Update(_ context.Context, id string, p webhook.SubscriptionPatch) error {
	if _, err := f.Get(context.Background(), id); err != nil {
		return err
	}
	if f.updated == nil {
		f.updated = map[string]webhook.SubscriptionPatch{}
	}
	f.updated[id] = p
	return nil
}

func (f *fakeWebhooks) List(context.Context) ([]webhook.ListedSubscription, error) {
	return f.subs, nil
}

func newWebhookRig(t *testing.T) (*ownerRig, *fakeWebhooks) {
	t.Helper()
	g := newOwnerRig(t, true)
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	f := &fakeWebhooks{subs: []webhook.ListedSubscription{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "Zapier", URL: "https://hooks.zapier.test/abc", Events: []string{"entity.published", "entity.updated", "form.submitted"}, Active: true, CreatedAt: at},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "Old CRM", URL: "http://crm.example.test/hk", Events: []string{"entity.deleted"}, AllowHTTP: true, CreatedAt: at},
	}}
	g.a.deps.Webhooks = f
	g.a.deps.WebhookDeliveryOn = true
	return g, f
}

func TestWebhooksListGoldenA11yAndBadges(t *testing.T) {
	t.Parallel()

	g, _ := newWebhookRig(t)
	rec := g.do("GET", "/admin/settings/webhooks", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("owner GET = %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	compareGolden(t, "pages/settings-webhooks.html", normalizePage(body))
	for _, want := range []string{"Zapier", "https://hooks.zapier.test/abc", "3 events", "1 event", "Active", "Paused", "Subscriptions", "Delivery log", ">Webhooks<"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(body, "not encrypted"); n != 1 {
		t.Errorf("not encrypted badge shown %d times, want once (the http:// row only)", n)
	}
	if strings.Contains(body, "webhooks-off") {
		t.Error("delivery-off notice shown while delivery is on")
	}
}

func TestWebhooksDeliveryOffNotice(t *testing.T) {
	t.Parallel()

	g, _ := newWebhookRig(t)
	g.a.deps.WebhookDeliveryOn = false
	if body := g.do("GET", "/admin/settings/webhooks", "owner-token", nil, nil).Body.String(); !strings.Contains(body, "delivery is switched off on this server") {
		t.Error("notice missing")
	}
}

func TestWebhooksPluginLeftNotice(t *testing.T) {
	t.Parallel()

	g, _ := newWebhookRig(t)
	const page = "/admin/settings/webhooks"
	if body := g.do("GET", page, "owner-token", nil, nil).Body.String(); strings.Contains(body, "webhooks-plugin") {
		t.Error("plugin notice shown with no plugin installed")
	}
	g.a.deps.WebhooksPluginLeft = func(context.Context) bool { return true }
	body := g.do("GET", page, "owner-token", nil, nil).Body.String()
	for _, want := range []string{"webhooks-plugin", "A plugin named webhooks is still installed", `href="/admin/plugins"`} {
		if !strings.Contains(body, want) {
			t.Errorf("notice lacks %q", want)
		}
	}
}

func TestWebhooksOwnerOnly(t *testing.T) {
	t.Parallel()

	g, _ := newWebhookRig(t)
	const p = "/admin/settings/webhooks"
	if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d, want 403", rec.Code)
	}
	if rec := g.do("GET", p, "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous GET = %d, want redirect to login", rec.Code)
	}
	if body := g.do("GET", "/admin/settings/media", "editor-token", nil, nil).Body.String(); strings.Contains(body, "/admin/settings/webhooks") {
		t.Error("editor sidebar links to Webhooks")
	}
	if body := g.do("GET", "/admin/settings/media", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `href="/admin/settings/webhooks"`) {
		t.Error("owner sidebar lacks Webhooks")
	}
}

func TestWebhooksUnavailableWithoutStore(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	if rec := g.do("GET", "/admin/settings/webhooks", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("no store: %d, want 404", rec.Code)
	}
}

var webhookForm1 = url.Values{
	"name": {"Zapier"}, "url": {"https://hooks.zapier.test/abc"}, "events": {"entity.published", "form.submitted"}, "active": {"true"},
	"max_retries": {"2"}, "retry_delay_seconds": {"30"}, "timeout_seconds": {"4"}, "headers": {"X-Team: ops\nX-Env: prod"},
}

var fieldName = regexp.MustCompile(`<(?:input|textarea|select)[^>]*\bname="([^"]+)"`)

func formFieldNames(body string) []string {
	var out []string
	for _, m := range fieldName.FindAllStringSubmatch(body, -1) {
		if m[1] != "_csrf" && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return out
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	s = s[i:]
	if j := strings.Index(s, to); j >= 0 {
		return s[:j]
	}
	return s
}

func TestWebhookDialogAndNewPageShareFields(t *testing.T) {
	t.Parallel()

	g, _ := newWebhookRig(t)
	list := g.do("GET", "/admin/settings/webhooks", "owner-token", nil, nil).Body.String()
	if !strings.Contains(list, `data-hm-open="new-webhook"`) || !strings.Contains(list, `href="/admin/settings/webhooks/new"`) || !strings.Contains(list, `id="new-webhook"`) {
		t.Fatalf("list lacks the header link or the dialog")
	}
	page := g.do("GET", "/admin/settings/webhooks/new", "owner-token", nil, nil)
	if page.Code != 200 {
		t.Fatalf("new page: %d", page.Code)
	}
	dialog := formFieldNames(between(list, `id="new-webhook-form"`, "</form>"))
	standalone := formFieldNames(between(page.Body.String(), `<form method="post" action="/admin/settings/webhooks"`, "</form>"))
	if len(dialog) == 0 || !slices.Equal(dialog, standalone) {
		t.Errorf("dialog fields %v, /new fields %v", dialog, standalone)
	}
	for _, want := range []string{"name", "url", "events", "active", "allow_http", "max_retries", "retry_delay_seconds", "timeout_seconds", "headers"} {
		if !slices.Contains(dialog, want) {
			t.Errorf("form lacks %q", want)
		}
	}
	if !strings.Contains(page.Body.String(), `value="entity.published"`) || !strings.Contains(page.Body.String(), "Allow plain http (payload and signature travel unencrypted)") {
		t.Error("event checkboxes or the plain http switch missing")
	}
	compareGolden(t, "pages/settings-webhooks-new.html", normalizePage(page.Body.String()))
}

func TestWebhookCreateRedirectsAndShowsSecretOnce(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	rec := g.do("POST", "/admin/settings/webhooks", "owner-token", webhookForm1, nil)
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if rec.Code != 303 || err != nil || u.Path != "/admin/settings/webhooks" || u.Query().Get("created") == "" {
		t.Fatalf("create: %d %s", rec.Code, loc)
	}
	if strings.Contains(loc, "whsec_") {
		t.Error("the secret is in the redirect URL")
	}
	if len(f.created) != 1 {
		t.Fatalf("created %d", len(f.created))
	}
	got := f.created[0]
	if got.MaxRetries != 2 || got.RetryDelaySeconds != 30 || got.TimeoutSeconds != 4 || !got.Active || got.Headers["X-Env"] != "prod" || len(got.Events) != 2 {
		t.Errorf("created subscription = %+v", got)
	}
	body := g.do("GET", loc, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, fakeSecret) || !strings.Contains(body, "Signing secret for Zapier") || !strings.Contains(body, `data-hm-copy-field="secret-value"`) {
		t.Error("banner with the copy button missing")
	}
	if row := strings.Index(body, "hm-row--new"); row < 0 || row > strings.Index(body, "Old CRM") {
		t.Error("the new row is not first")
	}
	again := g.do("GET", loc, "owner-token", nil, nil).Body.String()
	if strings.Contains(again, "THE-ONLY-SECRET") || strings.Contains(again, "secret-banner") || !strings.Contains(again, "can no longer be displayed") {
		t.Error("the secret came back on reload, or no used-up notice")
	}
	if strings.Contains(g.do("GET", "/admin/settings/webhooks/"+got.ID+"/edit", "owner-token", nil, nil).Body.String(), "THE-ONLY-SECRET") {
		t.Error("edit page shows the secret")
	}
}

func TestWebhookInvalidCreateIs422WithSummary(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	bad := url.Values{"name": {"CRM"}, "url": {"http://crm.example.test/hk"}, "events": {"entity.published"}, "max_retries": {"3"}, "retry_delay_seconds": {"60"}, "timeout_seconds": {"5"}}
	rec := g.do("POST", "/admin/settings/webhooks", "owner-token", bad, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "hm-formerrors") || !strings.Contains(body, "url must be https") || !strings.Contains(body, `value="http://crm.example.test/hk"`) {
		t.Fatalf("plain http: %d", rec.Code)
	}
	for name, form := range map[string]url.Values{
		"no events":   {"name": {"A"}, "url": {"https://a.example.test"}, "max_retries": {"3"}, "retry_delay_seconds": {"60"}, "timeout_seconds": {"5"}},
		"bad number":  {"name": {"A"}, "url": {"https://a.example.test"}, "events": {"entity.created"}, "max_retries": {"many"}, "retry_delay_seconds": {"60"}, "timeout_seconds": {"5"}},
		"bad header":  {"name": {"A"}, "url": {"https://a.example.test"}, "events": {"entity.created"}, "max_retries": {"3"}, "retry_delay_seconds": {"60"}, "timeout_seconds": {"5"}, "headers": {"no colon here"}},
		"forbidden":   {"name": {"A"}, "url": {"https://a.example.test"}, "events": {"entity.created"}, "max_retries": {"3"}, "retry_delay_seconds": {"60"}, "timeout_seconds": {"5"}, "headers": {"Host: x"}},
		"timeout big": {"name": {"A"}, "url": {"https://a.example.test"}, "events": {"entity.created"}, "max_retries": {"3"}, "retry_delay_seconds": {"60"}, "timeout_seconds": {"9"}},
	} {
		if rec := g.do("POST", "/admin/settings/webhooks", "owner-token", form, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "hm-formerrors") {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if len(f.created) != 0 {
		t.Errorf("an invalid form created %d webhooks", len(f.created))
	}
}

func TestWebhookEditAndUpdate(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	id := f.subs[0].ID
	page := g.do("GET", "/admin/settings/webhooks/"+id+"/edit", "owner-token", nil, nil)
	if body := page.Body.String(); page.Code != 200 || !strings.Contains(body, `value="Zapier"`) || !strings.Contains(body, "X-Team: ops") {
		t.Fatalf("edit page: %d", page.Code)
	}
	if rec := g.do("GET", "/admin/settings/webhooks/nope/edit", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown id: %d", rec.Code)
	}
	rec := g.do("POST", "/admin/settings/webhooks/"+id, "owner-token", webhookForm1, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/webhooks" {
		t.Fatalf("update: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if p, ok := f.updated[id]; !ok || *p.Name != "Zapier" || *p.MaxRetries != 2 || (*p.Headers)["X-Team"] != "ops" {
		t.Errorf("patch = %+v", p)
	}
	if rec := g.do("POST", "/admin/settings/webhooks/"+id, "owner-token", url.Values{"name": {""}}, nil); rec.Code != 422 {
		t.Errorf("invalid update: %d", rec.Code)
	}
}

func TestWebhookFormRoutesAreOwnerOnly(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	for _, p := range []string{"/admin/settings/webhooks/new", "/admin/settings/webhooks/" + f.subs[0].ID + "/edit"} {
		if rec := g.do("GET", p, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/admin/settings/webhooks", "/admin/settings/webhooks/" + f.subs[0].ID} {
		if rec := g.do("POST", p, "editor-token", webhookForm1, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d", p, rec.Code)
		}
	}
	if len(f.created) != 0 {
		t.Error("an editor created a webhook")
	}
}

const (
	zapierID = "11111111-1111-4111-8111-111111111111"
	crmID    = "22222222-2222-4222-8222-222222222222"
)

func flashOf(t *testing.T, g *ownerRig, loc string) string {
	t.Helper()
	return g.do("GET", loc, "owner-token", nil, nil).Body.String()
}

func TestWebhookRowActionsOfferPauseOrResume(t *testing.T) {
	t.Parallel()

	g, _ := newWebhookRig(t)
	body := g.do("GET", "/admin/settings/webhooks", "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`action="/admin/settings/webhooks/` + zapierID + `/pause"`, `action="/admin/settings/webhooks/` + crmID + `/resume"`,
		`action="/admin/settings/webhooks/` + zapierID + `/test"`, `href="/admin/settings/webhooks/` + zapierID + `/new-secret"`,
		`href="/admin/settings/webhooks/` + zapierID + `/delete"`, "Send test", "New secret",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `/`+zapierID+`/resume"`) || strings.Contains(body, `/`+crmID+`/pause"`) {
		t.Error("a row offers both Pause and Resume")
	}
}

func TestWebhookPauseAndResume(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	if rec := g.do("POST", "/admin/settings/webhooks/"+zapierID+"/pause", "owner-token", nil, nil); rec.Code != 303 || f.subs[0].Active {
		t.Errorf("pause: %d active=%v", rec.Code, f.subs[0].Active)
	}
	if rec := g.do("POST", "/admin/settings/webhooks/"+zapierID+"/resume", "owner-token", nil, nil); rec.Code != 303 || !f.subs[0].Active {
		t.Errorf("resume: %d active=%v", rec.Code, f.subs[0].Active)
	}
	if rec := g.do("POST", "/admin/settings/webhooks/nope/pause", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown id: %d", rec.Code)
	}
}

func TestWebhookNewSecretShowsItOnce(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	rec := g.do("POST", "/admin/settings/webhooks/"+zapierID+"/new-secret", "owner-token", nil, nil)
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || f.regen != 1 || strings.Contains(loc, "whsec_") {
		t.Fatalf("new secret: %d %s regen=%d", rec.Code, loc, f.regen)
	}
	if body := flashOf(t, g, loc); !strings.Contains(body, "whsec_REGENERATED") || !strings.Contains(body, "Signing secret for Zapier") {
		t.Error("banner missing")
	}
	if body := flashOf(t, g, loc); strings.Contains(body, "whsec_REGENERATED") {
		t.Error("the new secret came back on reload")
	}
	if body := g.do("GET", "/admin/settings/webhooks", "owner-token", nil, nil).Body.String(); strings.Contains(body, "whsec_") {
		t.Error("the plain list shows a secret")
	}
}

func TestWebhookDeleteAndConfirmPagesWorkWithoutJS(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	del := g.do("GET", "/admin/settings/webhooks/"+crmID+"/delete", "owner-token", nil, nil)
	body := del.Body.String()
	if del.Code != 200 || !strings.Contains(body, "Delete webhook &#34;Old CRM&#34;?") || !strings.Contains(body, `action="/admin/settings/webhooks/`+crmID+`/delete"`) || !strings.Contains(body, "delivery log") {
		t.Fatalf("delete confirm: %d", del.Code)
	}
	compareGolden(t, "pages/settings-webhooks-delete-confirm.html", normalizePage(body))
	sec := g.do("GET", "/admin/settings/webhooks/"+zapierID+"/new-secret", "owner-token", nil, nil)
	if sec.Code != 200 || !strings.Contains(sec.Body.String(), "Receivers must be updated to the new secret") || f.regen != 0 {
		t.Errorf("new-secret confirm: %d regen=%d (a GET must change nothing)", sec.Code, f.regen)
	}
	for _, p := range []string{"/admin/settings/webhooks/nope/delete", "/admin/settings/webhooks/" + zapierID + "/bogus"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
	if len(f.subs) != 2 {
		t.Fatal("a GET deleted a webhook")
	}
	if rec := g.do("POST", "/admin/settings/webhooks/"+crmID+"/delete", "owner-token", nil, nil); rec.Code != 303 || len(f.subs) != 1 || f.subs[0].ID != zapierID {
		t.Errorf("delete: %d subs=%d", rec.Code, len(f.subs))
	}
}

func TestWebhookSendTestReportsTheAnswer(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	status := 200
	f.test = webhook.DeliveryRow{ID: "del-1", Status: "delivered", ResponseStatus: &status}
	rec := g.do("POST", "/admin/settings/webhooks/"+zapierID+"/test", "owner-token", nil, nil)
	if rec.Code != 303 || f.tested != 1 {
		t.Fatalf("test: %d tested=%d", rec.Code, f.tested)
	}
	if got := flashText(t, rec); got != "Test sent (delivery del-1): answer HTTP 200." {
		t.Errorf("flash = %q", got)
	}
	f.test = webhook.DeliveryRow{ID: "del-2", Status: "failed", Error: "subscription removed or paused"}
	if got := flashText(t, g.do("POST", "/admin/settings/webhooks/"+crmID+"/test", "owner-token", nil, nil)); !strings.Contains(got, "paused") {
		t.Errorf("paused flash = %q", got)
	}
	f.test = webhook.DeliveryRow{ID: "del-3", Status: "failed", Error: "connection refused"}
	if got := flashText(t, g.do("POST", "/admin/settings/webhooks/"+zapierID+"/test", "owner-token", nil, nil)); !strings.Contains(got, "connection refused") {
		t.Errorf("failure flash = %q", got)
	}
	if rec := g.do("GET", "/admin/settings/webhooks/"+zapierID+"/test", "owner-token", nil, nil); rec.Code != 404 || f.tested != 3 {
		t.Errorf("GET test: %d tested=%d (a GET must not call a receiver)", rec.Code, f.tested)
	}
}

func TestWebhookActionsAreOwnerOnly(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	for _, a := range []string{"pause", "resume", "test", "new-secret", "delete"} {
		if rec := g.do("POST", "/admin/settings/webhooks/"+zapierID+"/"+a, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d", a, rec.Code)
		}
	}
	for _, a := range []string{"new-secret", "delete"} {
		if rec := g.do("GET", "/admin/settings/webhooks/"+zapierID+"/"+a, "editor-token", nil, nil); rec.Code != 403 {
			t.Errorf("editor GET %s = %d", a, rec.Code)
		}
	}
	if f.tested+f.regen != 0 || len(f.subs) != 2 {
		t.Error("an editor changed something")
	}
}

func withDeliveries(f *fakeWebhooks) {
	ok, bad := 200, 500
	at := time.Date(2026, 10, 2, 8, 30, 0, 0, time.UTC)
	f.deliveries = []webhook.DeliveryRow{
		{ID: "d-ok", SubscriptionID: zapierID, Event: "entity.published", Status: "delivered", Attempts: 1, MaxAttempts: 4, ResponseStatus: &ok, CreatedAt: at},
		{ID: "d-bad", SubscriptionID: zapierID, Event: "entity.updated", Status: "failed", Attempts: 4, MaxAttempts: 4, ResponseStatus: &bad, Error: "receiver answered HTTP 500", ResponseBody: "SECRET-BODY", CreatedAt: at.Add(-time.Hour)},
		{ID: "d-wait", SubscriptionID: crmID, Event: "form.submitted", Status: "pending", Attempts: 0, MaxAttempts: 4, CreatedAt: at.Add(-2 * time.Hour)},
		{ID: "d-err", SubscriptionID: crmID, Event: "entity.deleted", Status: "failed", Attempts: 1, MaxAttempts: 1, Error: strings.Repeat("x", 200), CreatedAt: at.Add(-3 * time.Hour)},
	}
}

func TestWebhookDeliveryLogTab(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	withDeliveries(f)
	rec := g.do("GET", "/admin/settings/webhooks/deliveries?subscription="+zapierID+"&page=2", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("deliveries: %d", rec.Code)
	}
	if f.gotSub != zapierID || f.gotLimit != 50 || f.gotOffset != 50 {
		t.Errorf("store called with %q limit %d offset %d", f.gotSub, f.gotLimit, f.gotOffset)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	for _, want := range []string{"When", "Event", "Status", "Attempts", "Answer", "entity.published", "1/4", "4/4", "HTTP 200", "HTTP 500", "Delivered", "Failed", "Pending", "Not sent yet", "2 Oct 2026 08:30"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, strings.Repeat("x", 100)) {
		t.Error("a long error is not clipped")
	}
	if strings.Contains(body, "SECRET-BODY") {
		t.Error("the response body is shown")
	}
	for _, id := range []string{"d-bad", "d-err"} {
		if !strings.Contains(body, `action="/admin/settings/webhooks/deliveries/`+id+`/retry"`) {
			t.Errorf("failed row %s has no Retry", id)
		}
	}
	for _, id := range []string{"d-ok", "d-wait"} {
		if strings.Contains(body, "deliveries/"+id+"/retry") {
			t.Errorf("row %s offers Retry", id)
		}
	}
	if g.a.deps.WebhookDeliveryOn = false; !strings.Contains(g.do("GET", "/admin/settings/webhooks/deliveries", "owner-token", nil, nil).Body.String(), "delivery is switched off on this server") {
		t.Error("delivery-off notice missing")
	}
	if rec := g.do("GET", "/admin/settings/webhooks/deliveries", "owner-token", nil, nil); f.gotSub != "" || f.gotOffset != 0 || rec.Code != 200 {
		t.Error("defaults: all subscriptions, first page")
	}
}

func TestWebhookDeliveryLogGolden(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	withDeliveries(f)
	body := g.do("GET", "/admin/settings/webhooks/deliveries", "owner-token", nil, nil).Body.String()
	compareGolden(t, "pages/settings-webhooks-deliveries.html", normalizePage(body))
}

func TestWebhookRetryRequeuesOnlyFailedRows(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	withDeliveries(f)
	rec := g.do("POST", "/admin/settings/webhooks/deliveries/d-bad/retry", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/webhooks/deliveries" || f.deliveries[1].Status != "pending" {
		t.Fatalf("retry failed row: %d %s status=%s", rec.Code, rec.Header().Get("Location"), f.deliveries[1].Status)
	}
	rec = g.do("POST", "/admin/settings/webhooks/deliveries/d-ok/retry", "owner-token", nil, nil)
	if rec.Code != 303 || f.deliveries[0].Status != "delivered" || flashText(t, rec) != "Only failed deliveries can be retried." {
		t.Errorf("retry delivered row: %d status=%s flash=%q", rec.Code, f.deliveries[0].Status, flashText(t, rec))
	}
	if rec := g.do("POST", "/admin/settings/webhooks/deliveries/nope/retry", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown delivery: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/webhooks/deliveries/d-bad/retry", "owner-token", nil, nil); rec.Code == 200 || len(f.retried) != 1 {
		t.Errorf("GET retry: %d retried=%v (retry must be a POST)", rec.Code, f.retried)
	}
}

func TestWebhookDeliveryRoutesAreOwnerOnly(t *testing.T) {
	t.Parallel()

	g, f := newWebhookRig(t)
	withDeliveries(f)
	if rec := g.do("GET", "/admin/settings/webhooks/deliveries", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/webhooks/deliveries/d-bad/retry", "editor-token", nil, nil); rec.Code != 403 || len(f.retried) != 0 {
		t.Errorf("editor POST retry = %d", rec.Code)
	}
}
