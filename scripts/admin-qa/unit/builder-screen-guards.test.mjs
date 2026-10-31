// screen.js is wired to the DOM and has no unit harness: these are source guards for wiring that a review found wrong.
// The behaviour behind them is tested where it lives (palette docPos/posValid, store ownsText).
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/web/admin/js/builder/screen.js", import.meta.url), "utf8");

test("the formatting bar does not wait for the elementsettings chunk", () => {
  assert.doesNotMatch(src, /Promise\.all\(\[[^\]]*fmtbar[^\]]*elementsettings/);
  assert.match(src, /linkPages \? linkPages\(/);
});

test("Enter (edittext) goes through ownsText, and a '+' that cannot be placed is refused", () => {
  const i = src.indexOf('case "edittext"');
  assert.match(src.slice(i, i + 200), /ownsText\(n\)/);
  assert.match(src, /const pos = docPos\(st\.doc, pt\);\s*if \(pos\) addAt\(pos, btn\);\s*else announce\(/);
});

test("the canvas toolbar's moves act on the whole section (moveSubject via selectionInfo), and its parent goes through onCrumb", () => {
  assert.match(src, /moveOp\(st\.doc, selectionInfo\(st\.doc, selected\)\?\.subject \?\? selected, action/);
  assert.match(src, /moveOp\(st\.doc, info\.subject, a, boot\.kinds\)/);
  assert.match(src, /onCrumb: \(id\) => select\(/);
});

test("toolbar Delete, Duplicate and Hide act on the same node as Move: the collapsed section's container", () => {
  const body = (name) => src.slice(src.indexOf(`function ${name}(`), src.indexOf("\n  }\n", src.indexOf(`function ${name}(`)));
  assert.match(body("removeSelected"), /selectionInfo\(st\.doc, selected\)\?\.subject \?\? selected/);
  assert.match(body("duplicateSelected"), /selectionInfo\(st\.doc, selected\)\?\.subject \?\? selected/);
  assert.doesNotMatch(body("removeSelected") + body("duplicateSelected"), /moveSubject/);
  assert.match(src, /const isHiddenAt = \(id\) => \{\s*const n = find\(st\.doc, hideTarget\(st\.doc, id, device\) \?\? id\)/);
});
