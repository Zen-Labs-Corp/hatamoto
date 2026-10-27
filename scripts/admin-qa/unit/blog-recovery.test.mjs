// The blog editor's recovery wiring: the delivered copy, the banner, writes on change, discard,
// the hold while the form submits, and the status lines. The autosave module is the real one;
// the other @hm modules are stubbed (Node cannot resolve the import map).
// Run: node --test scripts/admin-qa/unit/blog-recovery.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const js = new URL("../../../backend/web/admin/js/", import.meta.url);
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s === "@hm/dates") return { url: ${JSON.stringify(new URL("dates.js", js).href)}, shortCircuit: true };
      if (s === "@hm/editor/autosave") return { url: ${JSON.stringify(new URL("editor/autosave.js", js).href)}, shortCircuit: true };
      if (s.startsWith("@hm/")) return { url: "data:text/javascript,export const x=1;", shortCircuit: true };
      return next(s, c);
    }`),
);
const { BlogEditor, deliveredCopy } = await import(new URL("blogeditor.js", js));

globalThis.document = { createElement: () => ({ set innerHTML(v) { this.v = v; }, get innerHTML() { return this.v; } }) };

const el = () => {
  const handlers = {};
  return { hidden: true, textContent: "", handlers, addEventListener: (n, fn) => (handlers[n] = fn), click: () => handlers.click() };
};

/** An editor stand-in with the recovery client recorded. */
function editor({ copy = null, version = 4, opened = { name: "T", content: "<p>live</p>", excerpt: "", featured_image: "" }, autosave = null, fields = opened } = {}) {
  const calls = [];
  const box = el();
  const parts = { text: el(), use: el(), drop: el() };
  const inBox = { "[data-hm-be-recover-text]": parts.text, "[data-hm-be-recover-use]": parts.use, "[data-hm-be-recover-drop]": parts.drop };
  box.querySelector = (s) => inBox[s] ?? null;
  const form = {
    querySelector: (s) => (s === "[data-hm-be-recover]" ? box : null),
    elements: { namedItem: (n) => (n in fields ? { value: fields[n] } : null) },
  };
  const ed = {
    form,
    opened,
    version,
    autosave,
    unsaved: false,
    title: { value: "", style: {}, scrollHeight: 0 },
    countEl: null,
    say: (text, state) => calls.push(["say", text, state]),
    recovery: {
      copy,
      write: (c) => calls.push(["write", c]),
      clear: () => calls.push(["clear"]),
      suspend: () => calls.push(["suspend"]),
    },
    setBody: (h) => calls.push(["setBody", h]),
    sizeTitle() {},
    changed() {},
  };
  return { ed, calls, box, parts, form };
}

test("the delivered copy is read from #hm-be-recovery and checked", () => {
  const doc = (text) => ({ getElementById: (id) => (id === "hm-be-recovery" && text !== undefined ? { textContent: text } : null) });
  const c = deliveredCopy(doc(JSON.stringify({ content: "<p>x</p>", version: 3, name: "N", at: "2026-10-26T14:02:00Z", extra: "<img onerror=x>" })));
  assert.deepEqual(c, { content: "<p>x</p>", version: 3, name: "N", excerpt: "", featured_image: "", at: "2026-10-26T14:02:00Z" });
  assert.equal(deliveredCopy(doc(undefined)), null);
  assert.equal(deliveredCopy(doc("null")), null);
  assert.equal(deliveredCopy(doc("{")), null);
  assert.equal(deliveredCopy(doc(JSON.stringify({ content: 5 }))), null);
  assert.equal(deliveredCopy(doc(JSON.stringify({ content: "x", at: "soon" }))).at, "");
});

test("a planted localStorage copy is never read or offered", () => {
  const reads = [];
  globalThis.localStorage = { getItem: (k) => (reads.push(k), JSON.stringify({ content: "<p>PLANTED</p>", version: 4 })), key: () => null, length: 1 };
  const t = editor({ copy: null });
  BlogEditor.prototype.checkRecovery.call(t.ed);
  assert.equal(t.box.hidden, true, "no banner");
  assert.deepEqual(reads, []);
  assert.deepEqual(t.calls, [], "nothing to discard either");
  delete globalThis.localStorage;
});

test("a delivered copy that differs is offered; Restore applies it, Discard deletes it", () => {
  const copy = { content: "<p>typed</p>", version: 4, name: "Mine", excerpt: "E", at: "2026-10-26T14:02:00Z" };
  const t = editor({ copy, fields: { excerpt: "" } });
  t.ed.form.elements.namedItem = (n) => (n === "excerpt" ? t.excerptField : null);
  t.excerptField = { value: "" };
  BlogEditor.prototype.checkRecovery.call(t.ed);
  assert.equal(t.box.hidden, false);
  assert.match(t.parts.text.textContent, /were kept for you/);
  t.parts.use.click();
  assert.equal(t.ed.title.value, "Mine");
  assert.deepEqual(t.calls.find((c) => c[0] === "setBody"), ["setBody", "<p>typed</p>"]);
  assert.equal(t.excerptField.value, "E");
  assert.equal(t.box.hidden, true);

  const d = editor({ copy });
  BlogEditor.prototype.checkRecovery.call(d.ed);
  d.parts.drop.click();
  assert.deepEqual(d.calls, [["clear"]]);
  assert.equal(d.box.hidden, true);
});

test("a copy of an older version says the post has been saved since", () => {
  const t = editor({ copy: { content: "<p>typed</p>", version: 2, name: "T", at: "2026-10-26T14:02:00Z" } });
  BlogEditor.prototype.checkRecovery.call(t.ed);
  assert.match(t.parts.text.textContent, /has been saved since/);
});

test("a copy equal to the page is discarded without a banner", () => {
  const same = { content: "<p>live</p>", version: 4, name: "T", excerpt: "", at: "" };
  const t = editor({ copy: same });
  BlogEditor.prototype.checkRecovery.call(t.ed);
  assert.equal(t.box.hidden, true);
  assert.deepEqual(t.calls, [["clear"]]);
});

test("a change on a published post writes a copy; on a draft or back to the opened text it does not", () => {
  const changed = (fields, autosave) => {
    const t = editor({ fields, autosave });
    BlogEditor.prototype.changed.call(t.ed);
    return t.calls.filter((c) => c[0] === "write");
  };
  const typed = { name: "T", content: "<p>more</p>", excerpt: "", featured_image: "" };
  assert.deepEqual(changed(typed, null), [["write", { version: 4, ...typed }]]);
  assert.deepEqual(changed(typed, { change() {} }), [], "a draft has its autosave");
  assert.deepEqual(changed({ name: "T", content: "<p>live</p>", excerpt: "", featured_image: "" }, null), []);
});

test("submitting the form holds recovery writes; a preview does not", () => {
  const submit = (submitter) => {
    const t = editor();
    BlogEditor.prototype.submit.call(t.ed, { submitter });
    return t.calls.map((c) => c[0]);
  };
  assert.deepEqual(submit({ value: "update" }), ["suspend"]);
  assert.deepEqual(submit({ value: "preview" }), []);
});

test("the status line follows the server copy, and an offline line goes away once a write gets through", () => {
  const states = (s, unsaved = true, autosave = null) => {
    const t = editor({ autosave });
    t.ed.unsaved = unsaved;
    BlogEditor.prototype.recoveryState.call(t.ed, s);
    return t.calls;
  };
  assert.deepEqual(states("failed"), [["say", "Not saved. You appear to be offline.", "unsaved"]]);
  assert.deepEqual(states("kept"), [["say", "Unsaved changes", "unsaved"]], "the offline line is replaced");
  assert.deepEqual(states("kept", false), [], "nothing unsaved: leave the line alone");
  assert.deepEqual(states("expired"), [["say", "Your session ended: sign in again in another tab.", "error"]]);
  assert.match(states("refused")[0][1], /could not be kept/);
  assert.deepEqual(states("failed", true, {}), [], "a draft's status line is the autosave's");
});
