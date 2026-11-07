// The file upload field in the designer: palette entry, tile hint, inspector sections, preview validation.
// Run: node --test scripts/admin-qa/unit/forms-file.test.mjs
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
class El {
  constructor(tag) { this.tag = tag; this.attrs = {}; this.kids = []; this.on = {}; this.value = ""; this.dataset = {}; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  append(...k) { this.kids.push(...k); }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  type(v) { this.value = v; for (const f of this.on.input ?? []) f({}); }
  walk(out = []) { for (const k of this.kids) if (typeof k === "object") (out.push(k), k.walk(out)); return out; }
}
globalThis.document = { createElement: (t) => new El(t), createElementNS: (_n, t) => new El(t), querySelector: () => null };
const { TYPES, compatible, fileHint } = await import(new URL("forms/types.js", root));
const { paletteItems } = await import(new URL("forms/palette.js", root));
const { sectionsFor, createSections } = await import(new URL("forms/inspsections.js", root));
const { fieldSummary } = await import(new URL("forms/canvas.js", root));
const { validate } = await import(new URL("forms/validate.js", root));

const file = { id: "f", type: "file", label: "CV", name: "cv", validation: { accept: ["pdf", "docx"], max_mb: 5 } };

test("file is an advanced palette type with the upload icon", () => {
  assert.deepEqual([TYPES.file.group, TYPES.file.icon, TYPES.file.label], ["advanced", "upload", "File upload"]);
  const items = paletteItems([{ type: "text" }, { type: "file" }]);
  assert.deepEqual(items.map((i) => i.key), ["text", "file"]);
  assert.deepEqual(compatible("file"), []);
});

test("the tile hint lists extensions and the size limit, defaulting to 10 MB", () => {
  assert.equal(fileHint(file), ".pdf, .docx · up to 5 MB");
  assert.equal(fileHint({ type: "file", validation: { accept: ["png"] } }), ".png · up to 10 MB");
  assert.match(fileHint({ type: "file" }), /No file types chosen · up to 10 MB/);
});

test("a file field gets content, layout, rules and advanced", () => {
  assert.deepEqual(sectionsFor(file), ["content", "layout", "rules", "advanced"]);
  assert.match(fieldSummary({ ...file, required: true }), /File upload · required/);
});

test("the offline preview does not judge a file answer", () => {
  assert.deepEqual(validate([{ ...file, required: true }], {}).errors, {});
  assert.deepEqual(validate([file], { cv: "" }).data, {});
});

test("Allowed files and Largest file keep each other's edit when the sections are not rebuilt in between", () => {
  const field = { id: "f", type: "file", label: "CV", name: "cv", validation: { accept: ["pdf"] } };
  let validation = field.validation;
  const commit = (ops) => { for (const o of ops) if (o.key === "validation") validation = o.value; };
  const nodes = createSections({ field, fields: [field], commit }).flatMap((n) => [n, ...n.walk()]);
  const input = (suffix) => nodes.find((n) => n.tag === "input" && n.attrs.id?.endsWith(suffix));
  input("-accept").type("pdf, docx");
  input("-max_mb").type("5");
  assert.deepEqual(validation, { accept: ["pdf", "docx"], max_mb: 5 });
  input("-accept").type("png");
  assert.deepEqual(validation, { accept: ["png"], max_mb: 5 });
});
