package render

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/tags"
)

func testTagsConfig() TagsConfig {
	return TagsConfig{
		ExcludeSignedIn: true, Member: "hm_member",
		Vendors: []TagVendor{{ID: "ga4", Boot: "ga4", Category: "analytics", Src: "https://www.googletagmanager.com/gtag/js?id=G-ABC123XYZ9", Value: "G-ABC123XYZ9"}},
	}
}

func setTagsForTest(t *testing.T, c TagsConfig) {
	t.Helper()
	SetTags(func(context.Context) (TagsConfig, bool) { return c, true })
	t.Cleanup(func() { SetTags(nil) })
}

var tagsTagRE = regexp.MustCompile(`<script src="/theme/assets/hm-tags\.js\?v=[0-9a-f]{12}" defer></script>`)

func TestTagsTagOncePlacedInBodyAfterPluginBody(t *testing.T) {
	setTagsForTest(t, testTagsConfig())
	page := renderMinimal(t, nil)
	if n := strings.Count(page, "hm-tags.js"); n != 1 || !tagsTagRE.MatchString(page) {
		t.Fatalf("tag count = %d, page:\n%.600s", n, page)
	}
	if i, j := strings.Index(page, "hm-tags.js"), strings.Index(page, "<body"); i < j {
		t.Error("tag is not in the body")
	}
	if is := inlineScriptIssues(page); len(is) != 0 {
		t.Errorf("inline script issues: %v", is)
	}
	if strings.Contains(page, "<noscript>") && strings.Contains(page, "ns.html") {
		t.Error("noscript present without being configured")
	}
}

func TestTagsTagAbsentForEditorAndWhenOff(t *testing.T) {
	off := renderMinimal(t, nil)
	cfg := testTagsConfig()
	cfg.Noscript = `<iframe src="https://www.googletagmanager.com/ns.html?id=GTM-ABC123"></iframe>`
	setTagsForTest(t, cfg)
	for name, mut := range map[string]func(*PageView){
		"preview": func(v *PageView) { v.Preview = true },
		"canvas":  func(v *PageView) { v.Canvas = true },
	} {
		got := renderMinimal(t, mut)
		if strings.Contains(got, "hm-tags") || strings.Contains(got, "ns.html") {
			t.Errorf("%s frame carries tag markup", name)
		}
	}
	SetTags(nil)
	if got := renderMinimal(t, nil); got != off || strings.Contains(got, "hm-tags") {
		t.Error("output changed while tags are off")
	}
}

func TestTagsNoscriptInBody(t *testing.T) {
	cfg := testTagsConfig()
	cfg.Noscript = `<iframe src="https://www.googletagmanager.com/ns.html?id=GTM-ABC123" hidden></iframe>`
	setTagsForTest(t, cfg)
	page := renderMinimal(t, nil)
	i := strings.Index(page, "<noscript><iframe src=\"https://www.googletagmanager.com/ns.html?id=GTM-ABC123\"")
	if i < strings.Index(page, "<body") || i < 0 {
		t.Fatalf("noscript missing or outside body")
	}
}

func TestTagsTagHashTracksConfig(t *testing.T) {
	a, b := testTagsConfig(), testTagsConfig()
	b.Vendors = append(b.Vendors, TagVendor{ID: "meta", Boot: "meta"})
	c := testTagsConfig()
	c.Noscript = "<img>"
	if tagsMarkup(a) == tagsMarkup(b) || tagsMarkup(a) == tagsMarkup(c) || tagsMarkup(a) != tagsMarkup(testTagsConfig()) {
		t.Error("hash must change with the config and the noscript, and be stable otherwise")
	}
}

func TestTagsAssetServedAnd404WhenOff(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AssetPrefix+TagsScriptName, nil))
		return rec
	}
	if got := get().Code; got != http.StatusNotFound {
		t.Fatalf("off: status %d", got)
	}
	setTagsForTest(t, testTagsConfig())
	rec := get()
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "__HM_TAGS__") || !strings.Contains(body, `"value":"G-ABC123XYZ9"`) {
		t.Fatalf("status %d, body %.200s", rec.Code, body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" || strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q", cc)
	}
}

// Every vendor's Boot key needs a bootstrap in the script.
func TestEveryVendorHasABootEntry(t *testing.T) {
	t.Parallel()
	js := string(hmTagsSrc)
	for _, v := range tags.Vendors {
		if !regexp.MustCompile(`(?m)^    ` + v.Boot + `: function`).MatchString(js) {
			t.Errorf("hm_tags.js has no BOOT entry %q (vendor %s)", v.Boot, v.ID)
		}
	}
}

func TestTagsScriptContract(t *testing.T) {
	t.Parallel()
	js := string(hmTagsSrc)
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval("} {
		if strings.Contains(js, bad) {
			t.Errorf("script uses %q", bad)
		}
	}
	for _, bad := range []string{"'consent', 'default'", "'consent', 'update'"} {
		if strings.Contains(js, bad) {
			t.Errorf("script repeats the Consent Mode call %s", bad)
		}
	}
}

const tagsDOMHarness = `
const fs = require('fs');
const env = process.env;
const cfg = JSON.parse(env.CFG);
const listeners = {};
const scripts = [];
const calls = [];
const document = {
  cookie: env.MARKER === '1' ? 'a=b; hm_member=1' : 'a=b',
  head: { appendChild(s) { scripts.push(s.src); if (s.onload) s.onload(); } },
  documentElement: {},
  createElement: () => ({ setAttribute() {} }),
  addEventListener(t, f) { (listeners[t] = listeners[t] || []).push(f); },
  dispatchEvent(e) { (listeners[e.type] || []).forEach(f => f(e)); },
};
global.document = document;
global.window = global;
global.CustomEvent = function (type, o) { this.type = type; this.detail = o && o.detail; };
if (env.CONSENT === '1') {
  const state = { analytics: false, marketing: false, functional: false };
  global.hm = { consent: { allows: c => !!state[c], set: s => Object.assign(state, s) } };
}
global.fbq = function () { calls.push(['fbq', ...arguments]); };
global.fbq.queue = [];
global.UET = function () { this.push = function () { calls.push(['uet', ...arguments]); }; };
global.gtag = function () { calls.push(['gtag', ...arguments]); };
global.dataLayer = [];
global.lintrk = function () { calls.push(['lintrk', ...arguments]); };
global.ttq = [];
global.ttq.track = function () { calls.push(['ttq', ...arguments]); };
global.pintrk = function () { calls.push(['pintrk', ...arguments]); };
global.pintrk.queue = [];
global.clarity = function () { calls.push(['clarity', ...arguments]); };
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const out = { before: scripts.slice() };
if (env.CONSENT === '1') {
  global.hm.consent.set(JSON.parse(env.GRANT || '{}'));
  document.dispatchEvent(new CustomEvent('hm:consent', { detail: {} }));
  document.dispatchEvent(new CustomEvent('hm:consent', { detail: {} }));
}
out.after = scripts.slice();
calls.length = 0;
document.dispatchEvent(new CustomEvent('hm:conversion', { detail: { id: 'sub-123', form: 'contact', value: null } }));
out.calls = calls;
console.log(JSON.stringify(out));
`

func allVendorsConfig() TagsConfig {
	return TagsConfig{ExcludeSignedIn: true, Member: "hm_member", Vendors: []TagVendor{
		{ID: "ga4", Boot: "ga4", Category: "analytics", Src: "https://t.test/ga4", Value: "G-ABC123XYZ9"},
		{ID: "meta", Boot: "meta", Category: "marketing", Src: "https://t.test/meta", Value: "123456789012345"},
		{ID: "tawk", Boot: "tawk", Category: "functional", Src: "https://t.test/tawk", Value: "x/y"},
		{ID: "crisp", Boot: "crisp", Category: "functional", Src: "https://t.test/crisp", Value: "u"},
	}}
}

type tagsRun struct {
	Before, After []string
	Calls         [][]any
}

func runTagsDOM(t *testing.T, cfg TagsConfig, env ...string) tagsRun {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hm-tags.js")
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(script, tagsScript(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harness, []byte(tagsDOMHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := jsonx.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", harness, script)
	cmd.Env = append(os.Environ(), append(env, "CFG="+string(raw))...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var r tagsRun
	if err := jsonx.Unmarshal(out, &r, jsonx.StoredLoose); err != nil {
		t.Fatalf("bad harness output %s: %v", out, err)
	}
	return r
}

func TestTagsDOMNoLoadBeforeConsentThenOnceAfter(t *testing.T) {
	t.Parallel()
	r := runTagsDOM(t, allVendorsConfig(), "CONSENT=1", `GRANT={"analytics":true}`)
	if len(r.Before) != 0 {
		t.Errorf("loaded before consent: %v", r.Before)
	}
	if len(r.After) != 1 || r.After[0] != "https://t.test/ga4" {
		t.Errorf("after consent: %v (want only ga4, once, despite two events)", r.After)
	}
	r = runTagsDOM(t, allVendorsConfig(), "CONSENT=1", `GRANT={"analytics":true,"marketing":true,"functional":true}`)
	if len(r.After) != 4 {
		t.Errorf("all granted: %v", r.After)
	}
}

func TestTagsDOMWithoutConsentScriptLoadsAtOnce(t *testing.T) {
	t.Parallel()
	r := runTagsDOM(t, allVendorsConfig())
	if len(r.Before) != 4 {
		t.Errorf("loaded %v", r.Before)
	}
}

func TestTagsDOMBannerOnButConsentScriptAbsentLoadsNothing(t *testing.T) {
	t.Parallel()
	cfg := allVendorsConfig()
	cfg.Consent = true
	r := runTagsDOM(t, cfg)
	if len(r.Before) != 0 || len(r.After) != 0 {
		t.Errorf("loaded without a consent script while the banner is on: %+v", r)
	}
	if r := runTagsDOM(t, cfg, "CONSENT=1", `GRANT={"analytics":true}`); len(r.After) != 1 {
		t.Errorf("consent present and granted: %v", r.After)
	}
}

func TestTagsDOMMarkerSuppressesEverything(t *testing.T) {
	t.Parallel()
	r := runTagsDOM(t, allVendorsConfig(), "MARKER=1")
	if len(r.After) != 0 || len(r.Calls) != 0 {
		t.Errorf("signed-in visitor got tags: %+v", r)
	}
	cfg := allVendorsConfig()
	cfg.ExcludeSignedIn = false
	if r := runTagsDOM(t, cfg, "MARKER=1"); len(r.After) != 4 {
		t.Errorf("exclusion off but loaded %v", r.After)
	}
}

func TestTagsDOMOneConversionMapsPerVendor(t *testing.T) {
	t.Parallel()
	r := runTagsDOM(t, allVendorsConfig())
	var gotGA, gotMeta bool
	for _, c := range r.Calls {
		switch c[0] {
		case "gtag":
			gotGA = c[1] == "event" && c[2] == "generate_lead"
		case "fbq":
			if c[1] == "track" && c[2] == "Lead" {
				ids, _ := c[4].(map[string]any)
				gotMeta = ids["eventID"] == "sub-123"
			}
		default:
			t.Errorf("unexpected vendor call %v (chat vendors map nothing)", c)
		}
	}
	if !gotGA || !gotMeta || len(r.Calls) != 2 {
		t.Errorf("calls = %v", r.Calls)
	}
}

func TestTagsDOMConversionIgnoredForUnloadedVendors(t *testing.T) {
	t.Parallel()
	r := runTagsDOM(t, allVendorsConfig(), "CONSENT=1")
	if len(r.Calls) != 0 {
		t.Errorf("calls without consent: %v", r.Calls)
	}
}

// A tag pack vendor boots through "generic": its script loads once, only after
// its category is allowed, and a conversion maps to nothing.
func TestTagsDOMGenericPackVendorLoadsOnlyWhenAllowed(t *testing.T) {
	t.Parallel()
	cfg := TagsConfig{ExcludeSignedIn: true, Member: "hm_member", Vendors: []TagVendor{
		{ID: "acme.stats", Boot: "generic", Category: "analytics", Src: "https://t.test/acme-1.js", Value: "1"},
	}}
	r := runTagsDOM(t, cfg, "CONSENT=1", `GRANT={"marketing":true}`)
	if len(r.Before) != 0 || len(r.After) != 0 {
		t.Errorf("loaded for a category that was not granted: %+v", r)
	}
	r = runTagsDOM(t, cfg, "CONSENT=1", `GRANT={"analytics":true}`)
	if len(r.Before) != 0 || len(r.After) != 1 || r.After[0] != "https://t.test/acme-1.js" {
		t.Errorf("granted: %+v", r)
	}
	if len(r.Calls) != 0 {
		t.Errorf("generic vendor recorded a conversion: %v", r.Calls)
	}
}
