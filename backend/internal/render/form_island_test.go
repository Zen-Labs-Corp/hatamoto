package render

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// islandHarness runs widgets/form/island.js against a minimal fake form. The
// scenario arrives as JSON in SC; the script prints what it did as JSON.
const islandHarness = `
const vm = require('vm'), fs = require('fs');
const sc = JSON.parse(process.env.SC);
const out = { assigned: null, fetch: null, hidden: {}, replaced: false };
class RadioNodeList extends Array {}
const mk = (o) => Object.assign({ disabled: false, checked: false, setAttribute() {}, removeAttribute() {}, focus() {} }, o);
const inputs = (sc.inputs || []).map(mk);
const wrappers = (sc.rules || []).map((r) => {
  let input = inputs.find((i) => i.name === r.name);
  if (!input) { input = mk({ name: r.name, type: 'text', value: '' }); inputs.push(input); }
  return { hidden: false, input, dataset: { hmLogicField: r.field, hmLogicOp: r.op, hmLogicValue: r.value },
    querySelector: () => input, querySelectorAll: () => [input] };
});
const handlers = {};
const form = {
  elements: new Proxy({}, { get: (_, n) => {
    const f = inputs.filter((i) => i.name === n);
    return f.length === 0 ? undefined : f.length > 1 ? Object.assign(new RadioNodeList(), f) : f[0];
  } }),
  querySelectorAll: (sel) => (sel === '[data-hm-logic-field]' ? wrappers : []),
  querySelector: (sel) => (sel === '[data-hm-captcha]' && sc.captcha ? {} : null),
  getAttribute: () => '/forms/t/submit',
  addEventListener: (t, fn) => { handlers[t] = fn; },
  appendChild(el) { Object.assign(el, { disabled: false, checked: false }); inputs.push(el); return el; },
  submit() { out.nativeSubmit = true; },
  replaceWith() { out.replaced = true; },
};
class FormData {
  constructor() {}
  *[Symbol.iterator]() { for (const i of inputs) if (!/^(checkbox|radio)$/.test(i.type) || i.checked) yield [i.name, i.value]; }
}
const ctx = {
  RadioNodeList, FormData, URL, URLSearchParams, Set, Promise, console,
  CustomEvent: class { constructor(n, o) { this.type = n; this.detail = o && o.detail; } },
  document: { referrer: sc.referrer || '', querySelectorAll: () => [form], dispatchEvent() {}, createElement: () => ({}) },
  location: { href: sc.href || 'https://site.test/p', assign(u) { out.assigned = u; } },
  fetch: async (u, o) => { out.fetch = { u, headers: o.headers, body: String(o.body) }; return { ok: true, status: 201, json: async () => sc.reply || {} }; },
};
vm.runInNewContext(fs.readFileSync(process.argv[2], 'utf8'), ctx);
(async () => {
  if (sc.submit) await handlers.submit({ preventDefault() {} });
  wrappers.forEach((w) => { out.hidden[w.input.name] = w.hidden; });
  console.log(JSON.stringify(out));
})();
`

// islandVisibility runs one visibility case of the shared fixture through the
// island: the answers become form controls, the rules become logic wrappers.
func islandVisibility(t *testing.T, fields []map[string]any, data map[string]any) map[string]bool {
	t.Helper()
	nameOf := map[string]string{}
	for _, f := range fields {
		nameOf[f["id"].(string)], _ = f["name"].(string)
	}
	var inputs, rules []map[string]any
	for _, f := range fields {
		name, _ := f["name"].(string)
		ctl := map[string]any{"name": name, "type": "text"}
		if f["type"] == "checkbox" {
			ctl["type"] = "checkbox"
		}
		switch v := data[name].(type) {
		case []any:
			for _, m := range v {
				inputs = append(inputs, map[string]any{"name": name, "type": "checkbox", "value": m, "checked": true})
			}
		case string:
			ctl["value"], ctl["checked"] = v, true
			inputs = append(inputs, ctl)
		}
		if l, ok := f["logic"].(map[string]any); ok {
			rules = append(rules, map[string]any{"name": name, "field": nameOf[l["field_id"].(string)], "op": l["op"], "value": l["value"]})
		}
	}
	return runIsland(t, map[string]any{"inputs": inputs, "rules": rules}).Hidden
}

// The server and the island must agree on what is shown: both run the cases
// of the fixture the server's own test reads.
func TestFormIslandMatchesServerVisibilityCases(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "form", "testdata", "validation_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tbl struct {
		Visibility []struct {
			Name        string           `json:"name"`
			Fields      []map[string]any `json:"fields"`
			Data        map[string]any   `json:"data"`
			WantVisible map[string]bool  `json:"want_visible"`
		} `json:"visibility"`
	}
	if err := json.Unmarshal(raw, &tbl); err != nil {
		t.Fatal(err)
	}
	for _, tc := range tbl.Visibility {
		t.Run(tc.Name, func(t *testing.T) {
			hidden := islandVisibility(t, tc.Fields, tc.Data)
			for _, f := range tc.Fields {
				if f["logic"] == nil {
					continue
				}
				if got, want := !hidden[f["name"].(string)], tc.WantVisible[f["id"].(string)]; got != want {
					t.Errorf("field %v: island shows it = %v, server = %v", f["name"], got, want)
				}
			}
		})
	}
}

type islandRun struct {
	Assigned *string           `json:"assigned"`
	Fetch    *islandFetch      `json:"fetch"`
	Hidden   map[string]bool   `json:"hidden"`
	Inputs   map[string]string `json:"inputs"`
}

type islandFetch struct {
	U       string            `json:"u"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func runIsland(t *testing.T, scenario map[string]any) islandRun {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(harness, []byte(islandHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	sc, _ := json.Marshal(scenario)
	cmd := exec.Command("node", harness, filepath.Join("widgets", "form", "island.js"))
	cmd.Env = append(os.Environ(), "SC="+string(sc))
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, raw)
	}
	var out islandRun
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &out); err != nil {
		t.Fatalf("output %q: %v", raw, err)
	}
	return out
}

func TestFormIslandFollowsOnlySafeRedirects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		redirect string
		want     string // "" = must not navigate
	}{
		{"/thanks", "/thanks"},
		{"https://example.com/ok", "https://example.com/ok"},
		{"javascript:alert(1)", ""},
		{"JaVaScRiPt:alert(1)", ""},
		{"data:text/html,x", ""},
		{"vbscript:x", ""},
	}
	for _, c := range cases {
		out := runIsland(t, map[string]any{"submit": true, "reply": map[string]any{"success": true, "redirect_url": c.redirect}})
		got := ""
		if out.Assigned != nil {
			got = *out.Assigned
		}
		if got != c.want {
			t.Errorf("redirect %q navigated to %q, want %q", c.redirect, got, c.want)
		}
	}
}

func TestFormIslandPostsPageURLAndReferrer(t *testing.T) {
	t.Parallel()
	out := runIsland(t, map[string]any{
		"submit": true, "href": "https://site.test/c?utm=spring", "referrer": "https://search.test/q",
		"inputs": []map[string]any{{"name": "email", "type": "text", "value": "a@b.test"}},
		"reply":  map[string]any{"success": true},
	})
	if out.Fetch == nil {
		t.Fatal("no submit request")
	}
	q, err := url.ParseQuery(out.Fetch.Body)
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("hm_page") != "https://site.test/c?utm=spring" || q.Get("hm_ref") != "https://search.test/q" {
		t.Errorf("body = %s", out.Fetch.Body)
	}
}

// A theme's .hm-form layout (a grid with a gap, a submit button that keeps its
// width) must survive the widget: a rule that styles the form element itself
// may only apply to a form that has a half or third field.
func TestFormWidgetCSSLeavesThemeLayoutAlone(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("widgets", "form", "widget.css"))
	if err != nil {
		t.Fatal(err)
	}
	onForm := regexp.MustCompile(`\.hm-form(?:\s*>|\s*$|:)`)
	for _, line := range strings.Split(string(raw), "\n") {
		sel, _, ok := strings.Cut(line, "{")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "@media") {
			continue
		}
		for _, s := range strings.Split(strings.ReplaceAll(sel, ", ", ","), ",") {
			if strings.Contains(s, ":has(") {
				continue
			}
			if onForm.MatchString(strings.TrimSpace(s)) {
				t.Errorf("%q styles every form, so it overrides the theme", strings.TrimSpace(s))
			}
		}
	}
	if strings.Contains(string(raw), "__submit") {
		t.Error("the widget must not restyle the submit button")
	}
}
