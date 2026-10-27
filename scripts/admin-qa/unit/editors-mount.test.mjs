// editors.js: the module the data-hm-editor hook loads. It mounts the rich-text
// and html editors on first load and on every htmx swap. @hm/editor/mount is
// replaced by a recorder; the DOM is a minimal event target.
// Run: node --test scripts/admin-qa/unit/editors-mount.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const calls = [];
globalThis.__hmMounted = calls;
const listeners = {};
globalThis.document = { addEventListener: (type, fn) => (listeners[type] = fn) };

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s === "@hm/editor/mount") return { url: "data:text/javascript,export const enhanceEditors = (el) => globalThis.__hmMounted.push(el)", shortCircuit: true };
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
await import(new URL("backend/web/admin/js/editors.js", new URL("../../../", import.meta.url)));

test("mounts on the document at DOMContentLoaded", () => {
  listeners.DOMContentLoaded();
  assert.equal(calls.at(-1), globalThis.document);
});

test("mounts on the swapped fragment after an htmx swap", () => {
  const frag = { id: "swapped" };
  listeners["htmx:afterSettle"]({ target: frag });
  assert.equal(calls.at(-1), frag);
});
