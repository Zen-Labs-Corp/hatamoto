// A theme component's field on the v2 canvas still speaks protocol v1 (hm:edit, hm:edit-text, hm:rich-open).
// The host must route them to the screen's handlers and never throw when one is absent (it did).
// Run: node --test scripts/admin-qa/unit/canvas-host-v1.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { installDOM } from "./canvas-host-stub.mjs";

const dom = installDOM();
const { createCanvas } = await import(new URL("../../../backend/web/admin/js/canvas/host.js", import.meta.url));
const { fieldName, focusField } = await import(new URL("../../../backend/web/admin/js/builder/fieldjump.js", import.meta.url));

const rig = (handlers) => {
  const host = document.createElement("div");
  const canvas = createCanvas(host, { onSelect() {}, ...handlers }, { v2: true });
  dom.handshake(host, canvas);
  return host;
};
const edit = { hm: 1, type: "hm:edit", sectionId: "c1", path: "headline", value: "New", kind: "plaintext" };
const editText = { hm: 1, type: "hm:edit-text", sectionId: "c1", path: "items", index: 1, value: "B", expect: "b" };
const rich = { hm: 1, type: "hm:rich-open", sectionId: "c1", path: "body", rect: { top: 10, left: 20, width: 30, height: 40 } };

test("with no v1 handlers, the three messages are ignored without throwing", () => {
  const host = rig({});
  for (const m of [edit, editText, rich]) assert.doesNotThrow(() => dom.deliver(host, m));
});

test("with handlers, each message reaches its own (the rich rect scaled to the pane)", () => {
  const calls = [];
  const host = rig({ onEdit: (...a) => calls.push(["edit", ...a]), onEditText: (...a) => calls.push(["text", ...a]), onRichOpen: (...a) => calls.push(["rich", ...a]) });
  for (const m of [edit, editText, rich]) dom.deliver(host, m);
  assert.deepEqual(calls, [
    ["edit", "c1", "headline", "New"],
    ["text", "c1", "items", 1, "B", "b"],
    ["rich", "c1", "body", { top: 10, left: 20, width: 30, height: 40 }],
  ]);
});

test("fieldName is the component form's prefix plus the content path", () => {
  assert.equal(fieldName("c1", "faqs.0.question"), "n.c1.c.faqs.0.question");
});

test("focusField opens the Content tab, waits for the form to hold the field, then focuses its editor", async (t) => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  const log = [];
  const input = { getAttribute: () => "n.c1.c.body", closest: () => ({ querySelector: () => editor }), focus: () => log.push("input") };
  const editor = { scrollIntoView: () => log.push("scroll"), focus: () => log.push("editor") };
  let present = false;
  const root = {
    querySelector: (s) => (s === '[data-tab="content"]' ? { click: () => log.push("tab") } : null),
    querySelectorAll: () => (present ? [input] : []),
  };
  focusField(root, "c1", "body", (m) => log.push(m));
  t.mock.timers.tick(300);
  assert.deepEqual(log, ["tab"], "the form has not arrived");
  present = true;
  t.mock.timers.tick(100);
  assert.deepEqual(log, ["tab", "scroll", "editor", "Edit this field in the inspector"]);
});

test("focusField gives up after about 5 s and still says where to edit", (t) => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  const said = [];
  focusField({ querySelector: () => null, querySelectorAll: () => [] }, "c1", "x", (m) => said.push(m));
  t.mock.timers.tick(4900);
  assert.deepEqual(said, []);
  t.mock.timers.tick(200);
  assert.deepEqual(said, ["Edit this field in the inspector"]);
});
