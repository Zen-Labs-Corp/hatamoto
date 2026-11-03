// The box model numbers must fit four characters ("1280") so the nested rings still leave the centre room at 16rem.
// Run: node --test scripts/admin-qa/unit/builder-boxmodel-css.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../../../backend/web/admin/css/pages/builderinspector.css", import.meta.url), "utf8");

test("a box model number is at least 2.25rem wide (four characters)", () => {
  const rule = /^\.hm-bs-bm__n \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  const w = /inline-size:\s*([\d.]+)rem/.exec(rule);
  assert.ok(w, "the number sets an inline-size in rem");
  assert.ok(Number(w[1]) >= 2.25, `inline-size ${w[1]}rem cannot show four characters`);
});

test("forced colours: the numbers keep a visible edge and a refused one changes width and style", () => {
  const block = /@media \(forced-colors: active\) \{([\s\S]*?)\n\}/.exec(css)?.[1] ?? "";
  assert.match(block, /\.hm-bs-bm__n \{[^}]*border-color:\s*ButtonBorder/);
  assert.match(block, /\.hm-bs-bm__n\[aria-invalid="true"\] \{[^}]*border-width:\s*2px/);
});

test("an inherited choice option keeps a dashed border in the segmented track (forced colours draw borders, not shadows)", () => {
  const rule = /^\.hm-bs-style \.hm-bs-opt--inherited \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(rule, /border:\s*1px dashed var\(--color-hai\)/);
  assert.doesNotMatch(rule, /border:\s*0/);
});

test("the selected choice option has a semibold label (ADMIN_UI_RULES rule 6)", () => {
  const rule = /^\.hm-bs-style \.hm-bs-opt\[aria-pressed="true"\][^{]*\{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(rule, /font-weight:\s*var\(--weight-semibold\)/);
});

test("the ring caption has its own band above the top number and the size text wraps instead of overflowing", () => {
  const rings = /^\.hm-bs-bm__m, \.hm-bs-bm__p \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(rings, /grid-template-rows:\s*auto minmax\(0, 1fr\) auto;[^"]*grid-template-areas:\s*"\. t \."/, "the caption has its own auto grid row above the number row");
  assert.doesNotMatch(/^\.hm-bs-bm__t \{([^}]*)\}/m.exec(css)?.[1] ?? "", /position:\s*absolute/);
  const el = /^\.hm-bs-bm__el \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(css, /@container \(max-width: [\d.]+rem\) \{ \.hm-bs-bm__el \{ white-space: normal; \}/, "the size keeps one line while it fits and wraps at the multiplication sign only when it does not");
  assert.match(css, /@container \(max-width: [\d.]+rem\) \{ \.hm-bs-bm__el \{ font-size: var\(--text-2xs\)/, "a smaller token step comes before the wrap");
  assert.match(el, /overflow:\s*hidden/);
  assert.match(css, /^\.hm-bs-bm__c:has\(> \.hm-bs-bm__el\) \{[^}]*overflow:\s*hidden/m, "what does not fit is clipped, never painted over the side numbers");
});

test("a box model number has a visible resting edge from a token", () => {
  const rule = /^\.hm-bs-bm__n \{([^}]*)\}/m.exec(css)?.[1] ?? "";
  assert.match(rule, /border:\s*1px solid var\(--border\)/);
  assert.doesNotMatch(rule, /transparent/);
});

test("the ring caption total and the hint line have rules", () => {
  assert.match(css, /^\.hm-bs-bm__tot \{/m);
  assert.match(css, /^\.hm-bs-hint \{/m);
});

test("every class the compact fields and More toggles render has a rule and the label column is 4.5rem", () => {
  for (const cls of ["hm-bs-r2", "hm-bs-fldbox", "hm-bs-fld", "hm-bs-fld__lb", "hm-bs-fld__n", "hm-bs-fld__u", "hm-bs-more__w", "hm-bs-morebtn", "hm-bs-grp__more", "hm-bs-grp__hd", "hm-bs-classnote"]) {
    assert.ok(new RegExp("\\." + cls + "\\b").test(css), cls);
  }
  assert.match(css, /grid-template-columns:\s*4\.5rem minmax\(0, 1fr\)/);
});

test("the 28px field is the control height token and a scrub label drags sideways", () => {
  assert.match(/^\.hm-bs-style \.hm-bs-fld \{([^}]*)\}/m.exec(css)?.[1] ?? "", /block-size:\s*var\(--control-h-xs\)/);
  assert.match(css, /\.hm-bs-fld__lb\.hm-bs-scrub \{[^}]*cursor:\s*ew-resize/);
});

test("the context pill is coloured per device from the admin device tokens", () => {
  assert.match(css, /\.hm-bs-ctx__bp\[data-device="md"\] \{[^}]*var\(--color-device-tablet\)[^}]*var\(--color-device-tablet-soft\)/);
  assert.match(css, /\.hm-bs-ctx__bp\[data-device="sm"\] \{[^}]*var\(--color-device-phone\)[^}]*var\(--color-device-phone-soft\)/);
});

test("no literal colour, no text-transform", () => {
  assert.doesNotMatch(css, /#[0-9a-fA-F]{3,8}\b|\brgba?\(|text-transform/);
});
