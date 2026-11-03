// Inspector field mode: applyOnly hides the other top-level form blocks without removing or disabling a control, and the
// inspector draws one field's editor only. Run: node --test scripts/admin-qa/unit/builder-fieldmode.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { applyOnly, noImplicitSubmit } = await import(new URL("builder/fields.js", root));

// A just-enough form: top-level blocks with controls (name) and widgets (data-path).
const control = (name) => ({ getAttribute: (k) => (k === "name" ? name : null), disabled: false });
const block = (own, ...names) => {
  const kids = names.map(control);
  return { invalid: false, querySelector: function () { return this.invalid ? {} : null; }, hidden: false, style: { display: "" }, getAttribute: (k) => (k === "data-path" ? own : null), querySelectorAll: () => kids, kids };
};
const N = "n.c1.c.";
const mkForm = () => {
  const blocks = {
    group: block(null),
    eyebrow: block(null, `${N}eyebrow`),
    headline: block(null, `${N}headline`),
    ctas: block(`${N}ctas`, `_present.${N}ctas`, `${N}ctas.0.text`, `${N}ctas.0.url`, `_add.${N}ctas`),
    ctaUrl: block(null, `${N}ctaUrl`),
    legacy: block(null, `${N}kicker`),
  };
  const form = { children: Object.values(blocks) };
  // What a browser submits: every named, enabled control, hidden or not.
  form.names = () => Object.values(blocks).flatMap((b) => b.kids.filter((c) => !c.disabled).map((c) => c.getAttribute("name")));
  return { form, blocks };
};
const shown = (blocks) => Object.entries(blocks).filter(([, b]) => !b.hidden && b.style.display === "").map(([k]) => k);

test("applyOnly shows the block of the field and hides every other block and the group heading", () => {
  const { form, blocks } = mkForm();
  applyOnly(form, "c1", ["eyebrow"]);
  assert.deepEqual(shown(blocks), ["eyebrow"]);
  assert.equal(blocks.headline.style.display, "none");
  assert.equal(blocks.group.hidden, true, "a heading has no field of its own");
});

test("applyOnly keeps a list whole (its items, _present, _add markers) and does not mistake ctaUrl for ctas", () => {
  const { form, blocks } = mkForm();
  applyOnly(form, "c1", ["ctas"]);
  assert.deepEqual(shown(blocks), ["ctas"]);
});

test("applyOnly shows the block of an alias key too", () => {
  const { form, blocks } = mkForm();
  applyOnly(form, "c1", ["eyebrow", "kicker"]);
  assert.deepEqual(shown(blocks), ["eyebrow", "legacy"]);
});

test("applyOnly hides, never removes or disables: the submitted names stay complete, and re-applying after an adopt works", () => {
  const { form, blocks } = mkForm();
  const all = form.names();
  applyOnly(form, "c1", ["headline"]);
  assert.deepEqual(form.names(), all, "FormData still holds every name");
  assert.equal(form.children.length, 6, "no block was removed");
  Object.values(blocks).forEach((b) => ((b.hidden = false), (b.style.display = ""))); // adopt() put fresh markup in
  applyOnly(form, "c1", ["headline"]);
  assert.deepEqual(shown(blocks), ["headline"]);
});

test("the inspector draws field mode: header + that field's editor only, no tabs, class row or style; show() redraws on a field change", () => {
  const src = readFileSync(new URL("builder/inspector.js", root), "utf8");
  const fn = src.slice(src.indexOf("function drawField("), src.indexOf("function activate("));
  assert.match(fn, /contentPanel\(node, \[def\.key, \.\.\.alias\]\)/);
  assert.doesNotMatch(fn, /tablist|presetChunk|stylePanel|classHost/);
  assert.match(src, /if \(def\) return drawField\(node, def\);\n\s+const base/, "the tab shell is never built in field mode");
  assert.match(src, /\(api\.getField\?\.\(\)\?\.key \?\? null\) !== drawnKey/);
  const content = readFileSync(new URL("builder/inspcontent.js", root), "utf8");
  assert.match(content, /only: opts\.only \?\? null/);
  assert.match(content, /const props = opts\.only \? \[\] :/, "the section's own props (Title) stay out of a field's editor");
});

test("applyOnly keeps a block that carries a validation error visible", () => {
  const { form, blocks } = mkForm();
  blocks.headline.invalid = true;
  applyOnly(form, "c1", ["eyebrow"]);
  assert.deepEqual(shown(blocks), ["eyebrow", "headline"]);
});

test("Enter in a one-line input is cancelled so no hidden list button is pressed", () => {
  const key = (tag, type, k = "Enter") => {
    const e = { key: k, defaultPrevented: false, target: { tagName: tag, type }, preventDefault() { this.defaultPrevented = true; } };
    return [noImplicitSubmit(e), e.defaultPrevented];
  };
  assert.deepEqual(key("INPUT", "text"), [true, true]);
  assert.deepEqual(key("INPUT", "text", "a"), [false, false]);
  assert.deepEqual(key("TEXTAREA", "textarea"), [false, false], "a new line in a textarea");
  assert.deepEqual(key("BUTTON", "submit"), [false, false], "a repeater button still activates");
  assert.deepEqual(key("INPUT", "checkbox"), [false, false]);
  const form = {};
  const inner = (owner) => {
    const e = { key: "Enter", defaultPrevented: false, currentTarget: form, target: { tagName: "INPUT", type: "text", form: owner }, preventDefault() { this.defaultPrevented = true; } };
    return [noImplicitSubmit(e), e.defaultPrevented];
  };
  assert.deepEqual(inner(form), [true, true], "an input of the section form itself");
  assert.deepEqual(inner({}), [false, false], "an input of a nested form submits that form");
});
