package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// htmlTag returns the opening <html ...> tag of a rendered page.
func htmlTag(body string) string {
	i := strings.Index(body, "<html")
	if i < 0 {
		return ""
	}
	return body[i : i+strings.Index(body[i:], ">")+1]
}

// getWithDensity is rig.do for a GET that also carries the density cookie.
func getWithDensity(g *rig, path, token, density string) *httptest.ResponseRecorder {
	g.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: token})
	}
	req.AddCookie(&http.Cookie{Name: "hm_density", Value: density})
	g.addFetchMeta(req, token, nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func densityCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "hm_density" {
			return c
		}
	}
	return nil
}

func TestDensityDefaultsToComfortable(t *testing.T) {
	t.Parallel()

	g, _ := newProfileRig(t)
	rec := g.do("GET", "/admin/profile", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if strings.Contains(body, "data-admin-density") {
		t.Error("comfortable must render no density attribute")
	}
	if !strings.Contains(body, `id="display-card"`) || !strings.Contains(body, `action="/admin/profile/density"`) {
		t.Error("Display card missing")
	}
	if !strings.Contains(body, `value="comfortable" checked`) {
		t.Error("comfortable is not the checked card")
	}
}

func TestDensityPostSetsCookieAndRenders(t *testing.T) {
	t.Parallel()

	g, _ := newProfileRig(t)
	rec := g.do("POST", "/admin/profile/density", "owner-token", url.Values{"density": {"compact"}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/profile" {
		t.Fatalf("status %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	c := densityCookieOf(rec)
	if c == nil || c.Value != "compact" || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %+v", c)
	}
	if want := int(365 * 24 * time.Hour / time.Second); c.MaxAge != want {
		t.Errorf("MaxAge %d, want %d", c.MaxAge, want)
	}

	body := getWithDensity(g.rig, "/admin/profile", "owner-token", "compact").Body.String()
	if !strings.Contains(htmlTag(body), `data-admin-density="compact"`) {
		t.Errorf("shell layout lacks the attribute: %q", htmlTag(body))
	}
	if !strings.Contains(body, `value="compact" checked`) {
		t.Error("compact is not the checked card after saving it")
	}
}

func TestDensityAttributeOnEditorAndBareLayouts(t *testing.T) {
	t.Parallel()

	r := newBuilderRenderRig(t)
	for name, path := range map[string]string{
		"editor": r.url(r.page, "/edit"),
		"bare":   "/admin/login",
	} {
		token := "owner-token"
		if name == "bare" {
			token = ""
		}
		rec := getWithDensity(r.rig, path, token, "compact")
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		if tag := htmlTag(rec.Body.String()); !strings.Contains(tag, `data-admin-density="compact"`) {
			t.Errorf("%s layout: %q", name, tag)
		}
		if tag := htmlTag(getWithDensity(r.rig, path, token, "comfortable").Body.String()); strings.Contains(tag, "data-admin-density") {
			t.Errorf("%s layout renders an attribute for comfortable: %q", name, tag)
		}
	}
}

func TestDensityUnknownValueIsComfortable(t *testing.T) {
	t.Parallel()

	g, _ := newProfileRig(t)
	for _, v := range []string{"huge", "comfortable", ""} {
		rec := g.do("POST", "/admin/profile/density", "owner-token", url.Values{"density": {v}}, nil)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%q: status %d", v, rec.Code)
		}
		if c := densityCookieOf(rec); c == nil || c.Value != "comfortable" {
			t.Errorf("%q: cookie %+v, want comfortable", v, c)
		}
		body := getWithDensity(g.rig, "/admin/profile", "owner-token", "huge").Body.String()
		if strings.Contains(body, "data-admin-density") {
			t.Error("an unknown cookie value rendered an attribute")
		}
	}
}

func TestDensityPostNeedsCSRF(t *testing.T) {
	t.Parallel()

	g, _ := newProfileRig(t)
	rec := g.do("POST", "/admin/profile/density", "owner-token", url.Values{"density": {"compact"}}, map[string]string{"X-CSRF-Token": ""})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
	if c := densityCookieOf(rec); c != nil {
		t.Errorf("rejected request set a cookie: %+v", c)
	}
}
