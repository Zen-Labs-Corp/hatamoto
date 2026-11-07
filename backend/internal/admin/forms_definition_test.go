package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// putDefinition sends PUT /admin/forms/{id}/definition as the designer would.
func (g *rig) putDefinition(id, token, body string, hdr map[string]string) *httptest.ResponseRecorder {
	g.t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/admin/forms/"+id+"/definition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	}
	g.addFetchMeta(req, token, hdr)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

type definitionReplyBody struct {
	OK       bool   `json:"ok"`
	Version  string `json:"version"`
	SavedAt  string `json:"saved_at"`
	Conflict bool   `json:"conflict"`
	Error    string `json:"error"`
	Errors   []struct {
		FieldID string `json:"field_id"`
		Message string `json:"message"`
	} `json:"errors"`
}

func decodeDefinitionReply(t *testing.T, rec *httptest.ResponseRecorder) definitionReplyBody {
	t.Helper()
	var out definitionReplyBody
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &out, jsonx.StoredLoose); err != nil {
		t.Fatalf("reply %q: %v", rec.Body.String(), err)
	}
	return out
}

const definitionFields = `[{"id":"a","type":"text","label":"Name","name":"name","position":0},
	{"id":"b","type":"text","label":"Company","name":"company","position":1,"logic":{"field_id":"a","op":"filled"}}]`

func definitionBody(version, fields string) string {
	return `{"version":"` + version + `","name":"Contact","status":"active","fields":` + fields + `,"settings":{"submit_button_text":"Send","enable_captcha":false,"confirmation_email":false,"store_submissions":true}}`
}

func TestFormDefinitionSaveCSRFAndCache(t *testing.T) {
	t.Parallel()

	flushed := 0
	g, ff, _ := newFormRig(t, func(d *Deps) { d.Invalidate = func(context.Context) { flushed++ } })
	fm := ff.addForm("Contact", contactFields...)
	id := fm.ID.String()
	body := definitionBody(fm.Version(), definitionFields)

	// PUT is an unsafe method: without the token or the same-origin metadata it is refused.
	for name, hdr := range map[string]map[string]string{
		"no token":    {"X-CSRF-Token": ""},
		"wrong token": {"X-CSRF-Token": "forged"},
		"cross-site":  {"Sec-Fetch-Site": "cross-site"},
	} {
		if rec := g.putDefinition(id, "owner-token", body, hdr); rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
	}
	if flushed != 0 || ff.saves != 0 {
		t.Fatalf("a refused request wrote: flushed=%d saves=%d", flushed, ff.saves)
	}
	if rec := g.putDefinition(id, "", body, nil); rec.Code == http.StatusOK {
		t.Errorf("signed out: status = %d, want a refusal", rec.Code)
	}

	rec := g.putDefinition(id, "owner-token", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	out := decodeDefinitionReply(t, rec)
	if !out.OK || out.Version == "" || out.Version == fm.Version() && ff.saves == 0 || out.SavedAt == "" {
		t.Errorf("reply = %+v", out)
	}
	if flushed != 1 || ff.saves != 1 {
		t.Errorf("flushed=%d saves=%d, want 1 and 1", flushed, ff.saves)
	}
	got := ff.forms[fm.ID]
	if len(got.Fields) != 2 || got.Fields[1].Logic == nil || got.Fields[1].Logic.FieldID != "a" || got.Version() != out.Version {
		t.Errorf("stored = %+v (version %s)", got.Fields, got.Version())
	}
}

func TestFormDefinitionRejectsBadLogic(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	base := `{"id":"a","type":"text","label":"A","name":"a"}`
	for name, tc := range map[string]struct{ fields, wantID string }{
		"self reference": {`[{"id":"a","type":"text","label":"A","name":"a","logic":{"field_id":"a","op":"filled"}}]`, "a"},
		"missing source": {`[` + base + `,{"id":"b","type":"text","label":"B","name":"b","logic":{"field_id":"zz","op":"filled"}}]`, "b"},
		"two-field loop": {`[{"id":"a","type":"text","label":"A","name":"a","logic":{"field_id":"b","op":"filled"}},
			{"id":"b","type":"text","label":"B","name":"b","logic":{"field_id":"a","op":"filled"}}]`, "a"},
		"three-field loop": {`[{"id":"a","type":"text","label":"A","name":"a","logic":{"field_id":"c","op":"filled"}},
			{"id":"b","type":"text","label":"B","name":"b","logic":{"field_id":"a","op":"filled"}},
			{"id":"c","type":"text","label":"C","name":"c","logic":{"field_id":"b","op":"filled"}}]`, ""},
	} {
		rec := g.putDefinition(fm.ID.String(), "owner-token", definitionBody(fm.Version(), tc.fields), nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422: %s", name, rec.Code, rec.Body.String())
			continue
		}
		out := decodeDefinitionReply(t, rec)
		if out.OK || len(out.Errors) == 0 || out.Errors[0].Message == "" || (tc.wantID != "" && out.Errors[0].FieldID != tc.wantID) || out.Errors[0].FieldID == "" {
			t.Errorf("%s: reply = %+v", name, out)
		}
	}
	if ff.saves != 0 {
		t.Errorf("an invalid definition was saved")
	}
}

func TestFormDefinitionSecondSaveWithSameVersionConflicts(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	body := definitionBody(fm.Version(), definitionFields)

	first := g.putDefinition(fm.ID.String(), "owner-token", body, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	current := decodeDefinitionReply(t, first).Version

	second := g.putDefinition(fm.ID.String(), "owner-token", body, nil)
	if second.Code != http.StatusConflict {
		t.Fatalf("second: %d %s", second.Code, second.Body.String())
	}
	out := decodeDefinitionReply(t, second)
	if !out.Conflict || out.Version != current || out.Error == "" {
		t.Errorf("conflict reply = %+v, want current version %s", out, current)
	}

	// With the current version it saves again.
	if rec := g.putDefinition(fm.ID.String(), "owner-token", definitionBody(current, definitionFields), nil); rec.Code != http.StatusOK {
		t.Errorf("third: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFormDefinitionRequestChecks(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	id := fm.ID.String()
	good := definitionBody(fm.Version(), definitionFields)

	for name, tc := range map[string]struct {
		id, body string
		want     int
	}{
		"not json":        {id, "nope", http.StatusBadRequest},
		"empty body":      {id, "", http.StatusBadRequest},
		"bad version":     {id, strings.Replace(good, fm.Version(), "yesterday", 1), http.StatusBadRequest},
		"oversize":        {id, `{"name":"` + strings.Repeat("x", formDefinitionMaxBody) + `"}`, http.StatusRequestEntityTooLarge},
		"unknown form":    {"00000000-0000-0000-0000-000000000001", good, http.StatusNotFound},
		"not a uuid":      {"nope", good, http.StatusNotFound},
		"blank name":      {id, strings.Replace(good, `"name":"Contact"`, `"name":"  "`, 1), http.StatusUnprocessableEntity},
		"bad status":      {id, strings.Replace(good, `"active"`, `"bogus"`, 1), http.StatusUnprocessableEntity},
		"bad notify":      {id, strings.Replace(good, `"settings":{`, `"settings":{"notify_email":"nope",`, 1), http.StatusUnprocessableEntity},
		"unsafe redirect": {id, strings.Replace(good, `"settings":{`, `"settings":{"success_redirect_url":"javascript:alert(1)",`, 1), http.StatusUnprocessableEntity},
		"duplicate name":  {id, definitionBody(fm.Version(), `[{"id":"a","type":"text","label":"A","name":"x"},{"id":"b","type":"text","label":"B","name":"x"}]`), http.StatusUnprocessableEntity},
		"too many fields": {id, definitionBody(fm.Version(), manyFields(61)), http.StatusUnprocessableEntity},
	} {
		if rec := g.putDefinition(tc.id, "owner-token", tc.body, nil); rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d: %.200s", name, rec.Code, tc.want, rec.Body.String())
		}
	}
	if ff.saves != 0 {
		t.Errorf("a refused request wrote %d times", ff.saves)
	}
}

func manyFields(n int) string {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"f` + strings.Repeat("x", i) + `","type":"divider"}`)
	}
	b.WriteString("]")
	return b.String()
}

// The Settings tab saves through the same PUT: the new keys are stored, problems
// are keyed settings.<key>, and a stale version is a conflict.
func TestFormDefinitionSettingsTab(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	id, first := fm.ID.String(), fm.Version()
	put := func(version, settings string) *httptest.ResponseRecorder {
		return g.putDefinition(id, "owner-token", `{"version":"`+version+`","name":"Contact","status":"active","fields":[],"settings":{`+settings+`}}`, nil)
	}

	rec := put(first, `"notify_email":"a@example.com, b@example.com","notify_subject":"New enquiry from {{name}}","honeypot_off":true,"rate_limit_per_hour":5,"blocked_words":["casino"],"retention_days":90,"no_ip":true`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	got := ff.forms[fm.ID].Settings
	if *got.NotifyEmail != "a@example.com, b@example.com" || !got.HoneypotOff || got.RateLimitPerHour != 5 || got.RetentionDays != 90 || !got.NoIP || len(got.BlockedWords) != 1 {
		t.Errorf("stored %+v", got)
	}

	for name, tc := range map[string]struct{ settings, key string }{
		"recipient": {`"notify_email":"nope"`, "settings.notify_email"},
		"redirect":  {`"success_redirect_url":"javascript:x"`, "settings.success_redirect_url"},
	} {
		rec := put(ff.forms[fm.ID].Version(), tc.settings)
		out := decodeDefinitionReply(t, rec)
		if rec.Code != http.StatusUnprocessableEntity || len(out.Errors) != 1 || out.Errors[0].FieldID != tc.key {
			t.Errorf("%s: %d %+v", name, rec.Code, out)
		}
	}

	stale := put(first, `"no_ip":false`)
	if out := decodeDefinitionReply(t, stale); stale.Code != http.StatusConflict || !out.Conflict {
		t.Errorf("stale: %d %+v", stale.Code, out)
	}
}
