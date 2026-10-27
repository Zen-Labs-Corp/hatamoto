// The navigator's "Hidden" pill: cloned from the server-rendered <template> with the visually
// hidden " on <device>" suffix kept, and a plain fallback (with a console.warn at mount) without it.
// Run: node --test scripts/admin-qa/unit/builder-navigator-badge.test.mjs
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

// A just-enough DOM: elements with attributes, children, append and a deep cloneNode.
function el(tag) {
  const e = {
    tag,
    attrs: {},
    kids: [],
    setAttribute(k, v) { this.attrs[k] = v; },
    append(...k) { this.kids.push(...k); },
    set textContent(v) { this.kids = [String(v)]; },
    cloneNode() {
      const c = el(this.tag);
      c.attrs = { ...this.attrs };
      c.kids = this.kids.map((k) => (typeof k === "string" ? k : k.cloneNode(true)));
      return c;
    },
    get text() { return this.kids.map((k) => (typeof k === "string" ? k : k.text)).join(""); },
    addEventListener() {},
  };
  return e;
}
globalThis.document = { createElement: el, activeElement: null };
const { hiddenBadge, mountNavigator } = await import(new URL("builder/navigator.js", root));

const dotPill = () => {
  const pill = el("span");
  pill.attrs.class = "hm-badge hm-badge--neutral";
  const dot = el("span");
  dot.attrs.class = "hm-badge__dot";
  pill.append(dot, "Hidden");
  return pill;
};
const template = (pill) => ({ content: { firstElementChild: pill } });

test("with the template the pill markup is cloned and the device suffix kept", () => {
  const tpl = template(dotPill());
  const pill = hiddenBadge(tpl, "base");
  assert.equal(pill.attrs.class, "hm-badge hm-badge--neutral");
  assert.equal(pill.kids[0].attrs.class, "hm-badge__dot", "the dot span is cloned");
  const suffix = pill.kids.at(-1);
  assert.equal(suffix.attrs.class, "hm-visually-hidden");
  assert.match(suffix.text, /^ on \S/);
  assert.match(pill.text, /^Hidden on /);
  assert.equal(tpl.content.firstElementChild.kids.length, 2, "the template itself is not modified");
});

test("without the template the row still reads Hidden on <device>", () => {
  const pill = hiddenBadge(null, "base");
  assert.equal(pill.attrs.class, undefined);
  assert.equal(pill.kids[0], "Hidden");
  assert.equal(pill.kids.at(-1).attrs.class, "hm-visually-hidden");
  assert.match(pill.text, /^Hidden on /);
});

test("mounting without the template warns", () => {
  const warns = [];
  const warn = console.warn;
  console.warn = (m) => warns.push(m);
  try {
    const mount = (builder) => {
      const rootEl = { closest: () => builder, contains: () => false, replaceChildren() {}, addEventListener() {} };
      mountNavigator(rootEl, { getDoc: () => ({}), getSelected: () => null, onSelect() {} });
    };
    mount({ querySelector: () => template(dotPill()) });
    assert.equal(warns.length, 0, "no warning when the template is there");
    mount({ querySelector: () => null });
    assert.equal(warns.length, 1);
    assert.match(warns[0], /data-hm-bs-hidden-badge/);
  } finally {
    console.warn = warn;
  }
});
