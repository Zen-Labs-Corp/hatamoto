package render

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
  const input = mk({ name: r.name, type: 'text', value: r.inputValue || '' });
  inputs.push(input);
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
