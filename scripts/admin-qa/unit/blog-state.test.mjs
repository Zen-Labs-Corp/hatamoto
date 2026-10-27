// The blog editor's save-state line and its error toasts. Node cannot resolve the @hm
// import map, so a resolver hook stubs the autosave module.
// Run: node --test scripts/admin-qa/unit/blog-state.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s === "@hm/dates") return { url: ${JSON.stringify(new URL("../../../backend/web/admin/js/dates.js", import.meta.url).href)}, shortCircuit: true };
      if (s.startsWith("@hm/")) return { url: "data:text/javascript,export const createAutosave=()=>({}),createRecovery=()=>({}),recoveryAction=()=>({});", shortCircuit: true };
      return next(s, c);
    }`),
);
const { stateLine, alertToast, deliveredCopy, BlogEditor } = await import(new URL("../../../backend/web/admin/js/blogeditor.js", import.meta.url));

test("errors and stopped autosaves are alerts, progress is not", () => {
  assert.equal(stateLine({ state: "error", message: "You appear to be offline." }, "10:00").alert, true);
  assert.equal(stateLine({ state: "stopped", message: "Published elsewhere." }, "10:00").alert, true);
  for (const state of ["pending", "saving", "saved", "conflict"]) assert.equal(stateLine({ state }, "10:00").alert, false, state);
  assert.equal(stateLine({ state: "saved" }, "10:00").text, "Saved at 10:00");
});

test("alertToast sends an error to the toast region", () => {
  const seen = [];
  alertToast("Upload failed.", { dispatchEvent: (e) => seen.push(e) });
  assert.equal(seen[0].type, "hm:toast");
  assert.deepEqual(seen[0].detail, { kind: "error", message: "Upload failed." });
});

test("one error toast per outage, however many retries", () => {
  const toasts = [];
  globalThis.document = { dispatchEvent: (e) => toasts.push(e.detail) };
  const ed = { say() {}, showConflict() {} };
  const paint = (q) => BlogEditor.prototype.paintState.call(ed, q);
  const err = { state: "error", message: "You appear to be offline." };
  paint({ state: "pending" });
  for (let i = 0; i < 4; i++) {
    paint({ state: "saving" });
    paint(err);
  }
  assert.equal(toasts.length, 1);
  paint({ state: "saved" });
  paint({ state: "saving" });
  paint(err);
  assert.equal(toasts.length, 2);
});

test("announcements use their own live region and leave the save indicator alone", () => {
  const status = { textContent: "Saved at 10:00" };
  const dot = { dataset: { state: "unsaved" } };
  const notice = { textContent: "" };
  const form = { toggleAttribute() {}, querySelector: () => ({ setAttribute() {} }) };
  const ed = { form, statusEl: status, stateEl: dot, noticeEl: notice };
  ed.say = BlogEditor.prototype.say;
  ed.notify = BlogEditor.prototype.notify;
  BlogEditor.prototype.focusMode.call(ed, true);
  assert.match(notice.textContent, /Focus mode on/);
  BlogEditor.prototype.focusMode.call(ed, false);
  assert.equal(notice.textContent, "Focus mode off.");
  assert.equal(status.textContent, "Saved at 10:00");
  assert.equal(dot.dataset.state, "unsaved");
});

test("the error line no longer claims a copy in this browser", () => {
  assert.equal(stateLine({ state: "error", message: "Not saved. You appear to be offline." }, "10:00").text, "Not saved. You appear to be offline. Retrying.");
});

test("the delivered recovery copy is read as data and checked", () => {
  const doc = (text) => ({ getElementById: (id) => (id === "hm-be-recovery" ? { textContent: text } : null) });
  const ok = deliveredCopy(doc(JSON.stringify({ content: "<p>x</p>", version: 4, name: "T", excerpt: "e", featured_image: "", at: "2026-10-26T14:02:11Z" })));
  assert.deepEqual(ok, { content: "<p>x</p>", version: 4, name: "T", excerpt: "e", featured_image: "", at: "2026-10-26T14:02:11Z" });
  assert.equal(deliveredCopy(doc("not json")), null);
  assert.equal(deliveredCopy(doc('{"content":5}')), null);
  assert.equal(deliveredCopy({ getElementById: () => null }), null);
  assert.equal(deliveredCopy(doc(JSON.stringify({ content: "x", at: "yesterday" }))).at, "");
});
