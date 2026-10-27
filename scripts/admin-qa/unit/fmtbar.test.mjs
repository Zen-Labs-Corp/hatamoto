// The formatting bar's pure parts: where it goes and what its link dialog accepts.
// Run: node --test scripts/admin-qa/unit/fmtbar.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
const { placeBar, linkInput, cmdsFor, HEADING_CMDS } = await import(new URL("../../../backend/web/admin/js/canvas/fmtbar.js", import.meta.url));

const box = { barW: 200, barH: 30, stageW: 800 };

test("placeBar: above the selection, scaled", () => {
  assert.deepEqual(placeBar({ rect: { x: 100, y: 200, w: 50, h: 20 }, scale: 0.5, ...box }), { top: 200 * 0.5 - 30 - 6, left: 50 });
});

test("placeBar: flips below when there is no room above", () => {
  assert.deepEqual(placeBar({ rect: { x: 10, y: 20, w: 50, h: 20 }, scale: 1, ...box }), { top: 46, left: 10 });
});

test("placeBar: stays inside the stage on the right and on the left", () => {
  assert.equal(placeBar({ rect: { x: 790, y: 100, w: 5, h: 10 }, scale: 1, ...box }).left, 600);
  assert.equal(placeBar({ rect: { x: -20, y: 100, w: 5, h: 10 }, scale: 1, ...box }).left, 0);
});

test("placeBar: flips below and clamps against the visible part of a scrolled stage", () => {
  const view = { top: 300, height: 400 };
  // the element starts above the visible top: below it would still be above the view, so it is pulled into the view
  assert.deepEqual(placeBar({ rect: { x: 10, y: 250, w: 50, h: 20 }, scale: 1, ...box, view }), { top: 300, left: 10 });
  // room above inside the view: above
  assert.equal(placeBar({ rect: { x: 10, y: 500, w: 50, h: 20 }, scale: 1, ...box, view }).top, 464);
  // the element at the bottom edge with no room above it in the view: stays inside the view
  assert.equal(placeBar({ rect: { x: 10, y: 320, w: 50, h: 20 }, scale: 1, ...box, view }).top, 346);
  assert.equal(placeBar({ rect: { x: 10, y: 690, w: 50, h: 30 }, scale: 1, ...box, view: { top: 300, height: 400 } }).top, 654);
});

test("linkInput applies what the shared href fixture allows and refuses everything it refuses", () => {
  const cases = JSON.parse(readFileSync(new URL("../../../backend/internal/entity/testdata/inline_href_cases.json", import.meta.url), "utf8"));
  const stricter = new Set(["sms:+1555"]); // the dialog offers http(s), mailto and tel only; Go keeps sms for stored markup
  for (const c of cases) {
    const r = linkInput(c.href);
    if (!c.ok) assert.ok(r.error, `refuses ${JSON.stringify(c.href)}`);
    else if (!stricter.has(c.href)) assert.equal(r.href, c.href.trim(), `accepts ${JSON.stringify(c.href)}`);
  }
});

test("linkInput: javascript: is refused with a message, an empty field too", () => {
  assert.match(linkInput("javascript:alert(1)").error, /http/);
  assert.match(linkInput("").error, /address/);
});

// A bar press keeps focus in the frame (pointerdown is cancelled); calling frame.focus() on top of that blurs the
// editor in Chromium and ended the edit before the command ran (found by builder-inline.mjs).
test("the bar refocuses the frame only when something else has focus", () => {
  const src = readFileSync(new URL("../../../backend/web/admin/js/canvas/fmtbar.js", import.meta.url), "utf8");
  assert.match(src, /document\.activeElement !== f\) f\.focus\(\)/);
});

test("a heading's bar is B, I, Link and Clear; a text keeps all seven", () => {
  assert.deepEqual(HEADING_CMDS, ["bold", "italic", "link", "clear"]);
  assert.deepEqual(cmdsFor("heading"), ["bold", "italic", "link", "clear"]);
  assert.deepEqual(cmdsFor("text"), ["bold", "italic", "underline", "strike", "code", "link", "clear"]);
  assert.equal(cmdsFor(undefined).length, 7, "no kind: the full bar");
});

test("the link field takes a page path or an anchor from the datalist", () => {
  assert.deepEqual(linkInput("/about"), { href: "/about" });
  assert.deepEqual(linkInput("#pricing"), { href: "#pricing" });
});
