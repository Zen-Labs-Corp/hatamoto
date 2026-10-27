package admin

import (
	"hatamoto.dev/core/internal/auth"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

var keyForm = url.Values{"name": {"CI"}, "scopes": {"read:entities"}, "expiry": {"30"}}

func TestAPIKeyNewPage(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("GET", "/admin/settings/api-keys/new", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="name"`) || !strings.Contains(body, "Create key") {
		t.Fatalf("new page: %d", rec.Code)
	}
	// Every scope has its help line, so a key's reach is clear before ticking.
	for _, scope := range auth.ValidScopes() {
		if scopeHelp[string(scope)] == "" {
			t.Errorf("scope %s has no help text", scope)
		}
	}
	if !strings.Contains(body, scopeHelp["write:entities"]) || !strings.Contains(body, `id="scope-1-help"`) {
		t.Error("the scope help lines are not rendered")
	}
	if list := list2(g); !strings.Contains(list, `data-hm-open="new-key"`) || !strings.Contains(list, `href="/admin/settings/api-keys/new"`) || !strings.Contains(list, `id="new-key"`) {
		t.Errorf("list lacks the header link or the dialog: %.600s", list)
	}
}

func TestAPIKeyCreateRedirectsAndShowsSecretOnce(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("POST", "/admin/settings/api-keys", "owner-token", keyForm, nil)
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if rec.Code != 303 || err != nil || u.Path != "/admin/settings/api-keys" {
		t.Fatalf("create: %d %s", rec.Code, loc)
	}
	if _, err := uuid.Parse(u.Query().Get("created")); err != nil {
		t.Fatalf("created= is not a UUID: %s", loc)
	}
	body := g.do("GET", loc, "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `role="status"`) || !strings.Contains(body, "hm_ab12_THE-ONLY-SECRET") || !strings.Contains(body, `data-hm-copy-field="secret-value"`) {
		t.Error("banner with the copy button missing")
	}
	if row := strings.Index(body, "hm-row--new"); row < 0 || row > strings.Index(body, "Deploy") {
		t.Error("the new row is not first")
	}
	again := g.do("GET", loc, "owner-token", nil, nil).Body.String()
	if strings.Contains(again, "THE-ONLY-SECRET") || strings.Contains(again, "secret-banner") {
		t.Error("the banner came back on reload")
	}
	if !strings.Contains(again, "This key was shown once and can no longer be displayed. Create a new secret if you lost it.") {
		t.Error("a used-up secret shows no notice")
	}
	if g.keys.created == nil || len(g.keys.created) != 1 {
		t.Errorf("created %d keys", len(g.keys.created))
	}
}

func TestAPIKeyInvalidCreateRerendersNewPage(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("POST", "/admin/settings/api-keys", "owner-token", url.Values{"scopes": {"read:entities"}}, nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "hm-formerrors") || !strings.Contains(body, "Enter a name") {
		t.Fatalf("invalid create: %d", rec.Code)
	}
}

func TestAPIKeyFragmentKeepsDialog(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("GET", "/admin/settings/api-keys", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "main"})
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `id="new-key"`) {
		t.Errorf("fragment: %d, doctype=%v, dialog=%v", rec.Code, strings.Contains(body, "<!doctype html>"), strings.Contains(body, `id="new-key"`))
	}
}

func TestAPIKeySecretURLRedirectsToList(t *testing.T) {
	t.Parallel()

	g := newOwnerRig(t, true)
	rec := g.do("GET", "/admin/settings/api-keys/secret?id=x", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/api-keys" {
		t.Errorf("secret alias: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}
