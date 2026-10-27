// The name dialog for Save as block / template: focus in the field, Cancel resolves null, an empty name
// is refused, a failing save stays open with the message (a tiny element stub stands in for the DOM).
// Run: node --test scripts/admin-qa/unit/builder-savedialog.test.mjs
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
  constructor(tag) { this.tag = tag; this.attrs = {}; this.kids = []; this.on = {}; this.textContent = ""; this.open = false; this.hidden = false; this.value = ""; }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === "value") this.value = String(v); if (k === "hidden") this.hidden = true; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...k) { this.kids.push(...k); }
  addEventListener(t, f) { (this.on[t] ??= []).push(f); }
  async fire(t, e = {}) { for (const f of this.on[t] ?? []) await f({ preventDefault() {}, ...e }); }
  focus() { globalThis.document.activeElement = this; }
  showModal() { this.open = true; }
  close() { this.open = false; this.fire("close"); }
  remove() { this.removed = true; }
  walk(out = []) { for (const k of this.kids) if (typeof k === "object") (out.push(k), k.walk(out)); return out; }
}
globalThis.document = { createElement: (t) => new El(t), querySelector: () => null, body: new El("body"), activeElement: null };
const { ask } = await import(new URL("builder/savedialog.js", root));

const parts = () => {
  const dlg = document.body.kids.at(-1);
  const all = dlg.walk();
  return { dlg, input: all.find((n) => n.tag === "input"), form: all.find((n) => n.tag === "form"), problem: all.find((n) => n.attrs.role === "alert"), buttons: all.filter((n) => n.tag === "button") };
};

test("opens modal with the name field focused; Cancel comes first, the primary action last", () => {
  const p = ask({ title: "Save as block", label: "Block name", ok: "Save block" });
  const { dlg, input, buttons } = parts();
  assert.equal(dlg.open, true);
  assert.equal(document.activeElement, input);
  assert.equal(dlg.attrs["aria-labelledby"], "hm-bs-save-t");
  assert.deepEqual(buttons.map((b) => b.textContent), ["Cancel", "Save block"]);
  assert.equal(buttons[1].attrs.type, "submit");
  buttons[0].fire("click");
  return p.then((v) => assert.equal(v, null));
});

test("an empty name is refused in the dialog; a name resolves trimmed and closes", async () => {
  const p = ask({ title: "t", label: "l", ok: "ok" });
  const { dlg, input, form, problem } = parts();
  input.value = "   ";
  await form.fire("submit");
  assert.equal(dlg.open, true);
  assert.equal(problem.textContent, "Enter a name.");
  input.value = "  Promo  ";
  await form.fire("submit");
  assert.equal(dlg.open, false);
  assert.equal(await p, "Promo");
});

test("a save that fails keeps the dialog open with the server's message, then works on retry", async () => {
  let n = 0;
  const p = ask({ title: "t", label: "l", ok: "ok", onSubmit: async () => { if (++n === 1) throw new Error("That name is taken."); } });
  const { dlg, input, form, problem, buttons } = parts();
  input.value = "Promo";
  await form.fire("submit");
  assert.equal(dlg.open, true);
  assert.equal(problem.textContent, "That name is taken.");
  assert.equal(input.attrs["aria-invalid"], "true");
  assert.equal(buttons[1].disabled, false, "buttons are back on");
  await form.fire("submit");
  assert.equal(await p, "Promo");
  assert.equal(n, 2);
});
