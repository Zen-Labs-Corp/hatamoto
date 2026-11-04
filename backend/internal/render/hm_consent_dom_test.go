package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// consentDOMHarness runs the served script against a tiny fake DOM. SANDBOX=1
// makes every cookie access throw, as it does on an opaque-origin page. It
// prints one JSON object describing what happened.
const consentDOMHarness = `
const fs = require('fs');
const sandbox = process.env.SANDBOX === '1';
class El {
  constructor(tag) { this.tagName = tag.toUpperCase(); this.children = []; this.parentNode = null;
    this.style = {}; this.className = ''; this.listeners = {}; this.attrs = {}; this.tabIndex = tag === 'button' || tag === 'input' ? 0 : -1;
    this.disabled = false; this.offsetHeight = 50; this.textContent = ''; this.checked = false; }
  get isConnected() { let n = this; while (n.parentNode) n = n.parentNode; return n === document.documentElement; }
  get firstChild() { return this.children[0] || null; }
  setAttribute(k, v) { this.attrs[k] = v; }
  appendChild(c) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this; this.children.push(c); return c; }
  insertBefore(c, ref) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this;
    const i = ref ? this.children.indexOf(ref) : this.children.length; this.children.splice(i, 0, c); return c; }
  removeChild(c) { this.children.splice(this.children.indexOf(c), 1); c.parentNode = null; return c; }
  addEventListener(t, f) { (this.listeners[t] = this.listeners[t] || []).push(f); }
  removeEventListener() {}
  contains(n) { for (; n; n = n.parentNode) if (n === this) return true; return false; }
  all() { return this.children.flatMap(c => [c, ...c.all()]); }
  querySelectorAll() { return this.all().filter(n => n.tabIndex >= 0); }
  focus() { document.activeElement = this; }
  click() { (this.listeners.click || []).forEach(f => f({ currentTarget: this, target: this, preventDefault() {} })); }
}
const document = {
  readyState: 'complete', activeElement: null, listeners: {},
  documentElement: new El('html'), body: new El('body'), head: new El('head'),
  createElement: t => new El(t), createTextNode: t => { const n = new El('#text'); n.textContent = t; return n; },
  getElementById: () => null, querySelector: () => null,
  addEventListener() {}, removeEventListener() {}, dispatchEvent() {},
  get cookie() { if (sandbox) throw new Error('SecurityError'); return ''; },
  set cookie(v) { if (sandbox) throw new Error('SecurityError'); },
};
document.documentElement.appendChild(document.head);
document.documentElement.appendChild(document.body);
document.body.appendChild(new El('main'));
global.document = document;
global.CustomEvent = function (n, o) { this.detail = o.detail; };
global.navigator = {};
global.window = global;
global.self = { origin: sandbox ? 'null' : 'https://example.test' };
global.location = { protocol: 'https:' };
global.localStorage = { getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); } };
global.fetch = () => Promise.resolve({ json: () => Promise.resolve({ show: true }) });
global.addEventListener = () => {};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const out = { banner: false, firstChild: '', pad: '', decided: null, err: '' };
setTimeout(() => {
  const b = document.body.all().find(n => n.className === 'hm-consent-banner');
  out.banner = !!b;
  out.firstChild = document.body.firstChild.className;
  out.pad = document.documentElement.style.scrollPaddingBottom || '';
  try {
    if (b && process.env.ACCEPT === '1') {
      b.all().find(n => n.textContent === 'Accept all').click();
      out.decided = window.hm.consent.get().decided;
    }
    if (process.env.OPEN === '1') {
      window.hm.consent.open();
      out.focused = document.activeElement.parentNode.children[1].textContent;
      out.focusedTag = document.activeElement && document.activeElement.tagName;
    }
  } catch (e) { out.err = String(e); }
  out.padAfter = document.documentElement.style.scrollPaddingBottom || '';
  console.log(JSON.stringify(out));
}, 20);
`

func runConsentDOM(t *testing.T, env ...string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	cfg := testConsentConfig()
	cfg.Region = "all"
	cfg.Strings = map[string]string{"accept": "Accept all", "analytics": "Analytics"}
	dir := t.TempDir()
	script := filepath.Join(dir, "hm-consent.js")
	harness := filepath.Join(dir, "harness.js")
	if err := os.WriteFile(script, consentScript(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harness, []byte(consentDOMHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", harness, script)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestConsentScriptSandboxedPage(t *testing.T) {
	t.Parallel()
	out := runConsentDOM(t, "SANDBOX=1", "ACCEPT=1")
	for _, want := range []string{`"banner":true`, `"decided":true`, `"err":""`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

func TestConsentDialogFocusStartsAtFirstCategory(t *testing.T) {
	t.Parallel()
	out := runConsentDOM(t, "OPEN=1")
	if !strings.Contains(out, `"focused":"Analytics"`) {
		t.Errorf("first focus not on Analytics: %s", out)
	}
}
