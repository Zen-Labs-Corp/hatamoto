// The builder top bar's markup: labelled pane toggles, Undo mirrored from Redo, the More menu order, device chip and tokens.
// Run: node --test scripts/admin-qa/unit/builder-topbar.test.mjs
import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";

const web = new URL("../../../backend/web/admin/", import.meta.url);
const html = readFileSync(new URL("pages/entities/builder.html", web), "utf8");
const tokens = readFileSync(new URL("css/tokens.css", web), "utf8");
const more = html.slice(html.indexOf('id="bs-more"'), html.indexOf("{{if .Shell.SaveLabel}}"));

test("pane toggles carry visible labels and no flip", () => {
  assert.match(html, /"Label" "Navigator" "Icon" "panel-left"/);
  assert.match(html, /"Label" "Inspector" "Icon" "settings"/);
  assert.doesNotMatch(html, /IconOnly" true "Icon" "panel-left"/);
  assert.doesNotMatch(html, /data-hm-bs-toggle" "(nav|insp)" "data-flip/);
  // one arrow icon serves Undo and Redo: Undo alone is mirrored (css .hm-bs__tools [data-flip]), so the two differ
  assert.equal(html.split("data-flip").length - 1, 1);
  assert.match(html, /"data-hm-bs-undo" "" "data-flip" ""/);
  assert.match(html, /aria-label="Resize the navigator pane"/);
  assert.doesNotMatch(html, /Structure/);
});

test("More menu order: Versions, Page settings, Site styles, Keyboard shortcuts, separator, Delete (page, block or part) last", () => {
  const at = ["Versions</a>", "Page settings</a>", "data-hm-bs-sitestyles", "Keyboard shortcuts</button>", 'role="separator"', "Delete {{$noun}}</a>"].map((n) => more.indexOf(n));
  assert.ok(at.every((i) => i >= 0), at.join());
  assert.deepEqual(at, [...at].sort((a, b) => a - b));
  assert.doesNotMatch(html, /data-hm-bs-device-item/);
});

test("device chip exists and the server badge is wrapped so JS can retire it", () => {
  assert.match(html, /data-hm-bs-device-chip/);
  assert.match(html, /<span data-hm-bs-status>/);
});

test("device tokens exist in light, dark and the auto-dark mirror", () => {
  for (const t of ["tablet", "tablet-soft", "phone", "phone-soft"]) assert.equal(tokens.split(`--color-device-${t}:`).length - 1, 3, t);
});
