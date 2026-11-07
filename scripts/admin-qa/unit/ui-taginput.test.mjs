// The tag-input behaviour against a minimal fake DOM. Run: node --test scripts/admin-qa/unit/ui-taginput.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { mountTagInput } = await import(new URL("ui/taginput.js", root));
const { parseAddresses } = await import(new URL("ui/emailaddr.js", root));

class El {
  constructor(attrs = {}) {
    this.attrs = { ...attrs };
    this.children = [];
    this.handlers = {};
    this.textContent = "";
    this.value = "";
    this.hidden = false;
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  addEventListener(t, f) { this.handlers[t] = f; }
  appendChild(c) { this.children.push(c); }
  replaceChildren() { this.children = []; }
  focus() {}
  get id() { return this.attrs.id || ""; }
}
function chipEl() {
  const text = new El();
  const rm = new El();
  const li = new El();
  li.querySelector = (s) => (s === "[data-hm-ti-text]" ? text : rm);
  li.cloneNode = () => chipEl();
  return Object.assign(li, { text, rm });
}
function make(list, onChange) {
  const input = new El({ "aria-describedby": "ti-hint" });
  const parts = {
    "[data-hm-ti-chips]": new El(),
    "[data-hm-ti-input]": input,
    "[data-hm-ti-err]": new El({ id: "ti-err" }),
    "[data-hm-ti-live]": new El(),
    "template[data-hm-ti-chip]": { content: { firstElementChild: chipEl() } },
  };
  const rootEl = new El();
  rootEl.querySelector = (s) => parts[s];
  const calls = [];
  const api = mountTagInput(rootEl, { list, onChange: (l) => (calls.push(l), onChange?.(l)) });
  const key = (k, extra = {}) => {
    const e = { key: k, isComposing: false, prevented: false, preventDefault() { this.prevented = true; }, ...extra };
    input.handlers.keydown(e);
    return e;
  };
  return { api, input, rootEl, calls, key, chips: parts["[data-hm-ti-chips]"], err: parts["[data-hm-ti-err]"], live: parts["[data-hm-ti-live]"] };
}
const texts = (m) => m.chips.children.map((c) => c.text.textContent);

test("parseAddresses splits, dedupes case-insensitively and rejects display names", () => {
  const r = parseAddresses("a@b.co, A@B.co; c@d.io\nNotAnEmail Name <x@y.z>");
  assert.deepEqual(r.valid, ["a@b.co", "c@d.io"]);
  assert.deepEqual(r.bad, ["NotAnEmail", "Name", "<x@y.z>"]);
});

test("enter and comma commit the typed address", () => {
  const m = make([]);
  m.input.value = "a@b.co";
  assert.equal(m.key("Enter").prevented, true);
  m.input.value = "c@d.io";
  m.key(",");
  assert.deepEqual(m.api.get(), ["a@b.co", "c@d.io"]);
  assert.deepEqual(texts(m), ["a@b.co", "c@d.io"]);
  assert.equal(m.input.value, "");
  assert.equal(m.calls.length, 2);
});

test("a composing key press does not commit", () => {
  const m = make([]);
  m.input.value = "a@b.co";
  m.key("Enter", { isComposing: true });
  assert.deepEqual(m.api.get(), []);
});

test("a duplicate is not added twice", () => {
  const m = make(["a@b.co"]);
  m.input.value = "A@B.CO";
  m.key("Enter");
  assert.deepEqual(m.api.get(), ["a@b.co"]);
  assert.equal(m.calls.length, 0);
});

test("an invalid address stays in the input with an error that clears on input", () => {
  const m = make([]);
  m.input.value = "nope";
  m.key("Enter");
  assert.equal(m.err.textContent, "nope is not a valid email address");
  assert.equal(m.err.hidden, false);
  assert.equal(m.input.getAttribute("aria-invalid"), "true");
  assert.match(m.input.getAttribute("aria-describedby"), /ti-err/);
  assert.equal(m.rootEl.getAttribute("data-invalid"), "");
  assert.equal(m.live.textContent, "nope is not a valid email address");
  assert.equal(m.input.value, "nope");
  m.input.handlers.input();
  assert.equal(m.err.hidden, true);
  assert.equal(m.input.getAttribute("aria-invalid"), null);
  assert.equal(m.input.getAttribute("aria-describedby"), "ti-hint");
  assert.equal(m.rootEl.getAttribute("data-invalid"), null);
});

test("paste of three mixed addresses chips the valid ones and returns the bad one", () => {
  const m = make([]);
  const e = { preventDefault() { this.prevented = true; }, clipboardData: { getData: () => "a@b.co, bad@, c@d.io" } };
  m.input.handlers.paste(e);
  assert.equal(e.prevented, true);
  assert.deepEqual(m.api.get(), ["a@b.co", "c@d.io"]);
  assert.equal(m.input.value, "bad@");
  assert.match(m.err.textContent, /^bad@ is not a valid email address$/);
});

test("backspace on an empty input removes the last chip, with a live message", () => {
  const m = make(["a@b.co", "c@d.io"]);
  m.key("Backspace");
  assert.deepEqual(m.api.get(), ["a@b.co"]);
  assert.equal(m.live.textContent, "Removed c@d.io");
  m.input.value = "x";
  m.key("Backspace");
  assert.deepEqual(m.api.get(), ["a@b.co"]);
});

test("chips use textContent and a labelled remove button", () => {
  const m = make(["a@b.co"]);
  const chip = m.chips.children[0];
  assert.equal(chip.rm.getAttribute("aria-label"), "Remove a@b.co");
  chip.rm.handlers.click();
  assert.deepEqual(m.api.get(), []);
});

test("blur commits pending text; set() re-renders without onChange", () => {
  const m = make([]);
  m.input.value = "a@b.co";
  m.input.handlers.blur();
  assert.deepEqual(m.api.get(), ["a@b.co"]);
  assert.equal(m.live.textContent, "Added a@b.co");
  m.calls.length = 0;
  m.api.set(["z@y.xx"]);
  assert.deepEqual(texts(m), ["z@y.xx"]);
  assert.equal(m.calls.length, 0);
});

test("addresses net/mail accepts are accepted: UTF-8 and underscores", () => {
  const { valid, bad } = parseAddresses("jörg@müller.de, a_b@sub_domain.example, 用户@例子.广告, nope@, @x.y");
  assert.deepEqual(valid, ["jörg@müller.de", "a_b@sub_domain.example", "用户@例子.广告"]);
  assert.deepEqual(bad, ["nope@", "@x.y"]);
});

test("a stored recipient the check cannot parse is kept, so the next edit does not drop it", () => {
  const m = make(["info@x.com", "legacy recipient"], () => {});
  assert.equal(m.api.get().length, 2);
  m.api.set(["info@x.com", "legacy recipient"]);
  assert.deepEqual(m.api.get(), ["info@x.com", "legacy recipient"]);
});
