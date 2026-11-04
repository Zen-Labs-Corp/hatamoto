// The builder top bar's markup: labelled pane toggles, Undo mirrored from Redo, the More menu order, device chip removal and tokens.
// Run: node --test scripts/admin-qa/unit/builder-topbar.test.mjs
import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";

const web = new URL("../../../backend/web/admin/", import.meta.url);
const html = readFileSync(new URL("pages/entities/builder.html", web), "utf8");
const screen = readFileSync(new URL("js/builder/screen.js", web), "utf8");
const tokens = readFileSync(new URL("css/tokens.css", web), "utf8");
const more = html.slice(html.indexOf('id="bs-more"'), html.indexOf("{{if .Shell.SaveLabel}}"));

test("pane toggles are icon-only, named and carry their shortcut", () => {
  assert.match(html, /"Label" "Navigator" "AriaLabel" "Navigator" "IconOnly" true "Icon" "panel-left"/);
  assert.match(html, /"Label" "Inspector" "AriaLabel" "Inspector" "IconOnly" true "Icon" "panel-right"/);
  for (const w of ["nav", "insp"]) assert.match(html, new RegExp(`"data-hm-bs-toggle" "${w}" "aria-expanded" "true" "aria-controls" "[^"]+" "title" "[^"]*Ctrl/Cmd\\+Alt`));
  assert.doesNotMatch(html, /data-hm-bs-toggle" "(nav|insp)" "data-flip/);
  // one arrow icon serves Undo and Redo: Undo alone is mirrored (css .hm-bs__tools [data-flip]), so the two differ
  assert.equal(html.split("data-flip").length - 1, 1);
  assert.match(html, /"data-hm-bs-undo" "" "data-flip" ""/);
  assert.match(html, /aria-label="Resize the navigator pane"/);
  assert.doesNotMatch(html, /Structure/);
});

test("device switch is its own bar child, outside the tools span, and hidden until JS", () => {
  const tools = html.slice(html.indexOf('<span class="hm-bs__tools"'), html.indexOf('"More actions"'));
  assert.doesNotMatch(tools, /hm-bs__devices/);
  assert.doesNotMatch(tools, /data-hm-bs-device/);
  assert.match(html, /<span class="hm-bs__devices" hidden data-hm-bs-js>/);
  assert.ok(html.indexOf("hm-bs__lead") < html.indexOf("hm-bs__devices") && html.indexOf("hm-bs__devices") < html.indexOf("hm-bs__end"));
  assert.equal(html.split('data-hm-bs-device"').length - 1, 3);
});

test("More menu order: Versions, Page settings, Site styles, Keyboard shortcuts, separator, Delete (page, block or part) last", () => {
  const at = ["Versions</a>", "Page settings</a>", "data-hm-bs-sitestyles", "Keyboard shortcuts</button>", 'role="separator"', "Delete {{$noun}}</a>"].map((n) => more.indexOf(n));
  assert.ok(at.every((i) => i >= 0), at.join());
  assert.deepEqual(at, [...at].sort((a, b) => a - b));
  assert.doesNotMatch(html, /data-hm-bs-device-item/);
});

test("the device chip is gone (the inspector context line is the one place) and the width change is still announced", () => {
  assert.doesNotMatch(html, /data-hm-bs-device-chip/);
  assert.doesNotMatch(screen, /device-chip/);
  assert.match(screen, /announce\(`Editing \$\{BP_NAME\[d\]\} styles\.`\)/);
  assert.match(html, /<h2 class="hm-bs__h hm-visually-hidden">Inspector<\/h2>/);
  assert.match(html, /<span data-hm-bs-status>/);
});

test("device tokens exist in light, dark and the auto-dark mirror", () => {
  for (const t of ["tablet", "tablet-soft", "phone", "phone-soft"]) assert.equal(tokens.split(`--color-device-${t}:`).length - 1, 3, t);
});

test("device buttons carry an icon, Fit opens a zoom menu, Preview and Shortcuts live where the mockup puts them", () => {
  for (const [l, i] of [["Desktop", "monitor"], ["Tablet", "tablet"], ["Phone", "smartphone"]]) assert.match(html, new RegExp(`"Label" "${l}" "Icon" "${i}"`));
  assert.match(html, /popovertarget" "bs-zoom"[^)]*"data-hm-bs-zoomtrigger"/);
  const zoom = html.slice(html.indexOf('id="bs-zoom"'), html.indexOf("</div>", html.indexOf('id="bs-zoom"')));
  assert.deepEqual([...zoom.matchAll(/data-zoom="(\w+)"/g)].map((m) => m[1]), ["fit", "50", "75", "100"]);
  assert.ok(html.indexOf("bs-zoom") < html.indexOf("data-hm-bs-undo"));
  assert.ok(html.indexOf("data-hm-bs-preview") > html.indexOf('class="hm-bs__panes"') && html.indexOf("data-hm-bs-preview") < html.indexOf('"More actions"'));
  // the one Shortcuts control is the More menu item; the icon button and its proxy are gone
  assert.equal(html.split("data-hm-bs-keys").length - 1, 1);
  assert.match(more, /data-hm-bs-always data-hm-bs-keys aria-haspopup="dialog" aria-keyshortcuts="\?">Keyboard shortcuts<\/button>/);
});
