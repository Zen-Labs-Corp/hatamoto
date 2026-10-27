// contentExtras on a just-enough DOM: mounting never commits, clearing the Link clears href+target+rel in one
// commit (one undo restores all three and the checkboxes), a bound href counts as a link.
// Run: node --test scripts/admin-qa/unit/builder-elementsettings-dom.test.mjs
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

const matches = (e, tok) => {
  const m = /^([a-z]*)((?:\[[^\]]+\])*)$/.exec(tok);
  if (m[1] && e.tag !== m[1]) return false;
  return [...m[2].matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)].every(([, k, v]) => (v === undefined ? k in e.attrs : e.attrs[k] === v));
};
function el(tag) {
  const e = {
    tag, attrs: {}, kids: [], parent: null, isConnected: true, listeners: [], hidden: false, disabled: false, checked: false, value: "",
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k] ?? null; },
    removeAttribute(k) { delete this.attrs[k]; },
    append(...k) { for (const c of k) { if (typeof c !== "string") c.parent = this; this.kids.push(c); } },
    after(n) { n.parent = this.parent; this.parent.kids.splice(this.parent.kids.indexOf(this) + 1, 0, n); },
    replaceChildren(...k) { this.kids = k; },
    set textContent(v) { this.kids = [String(v)]; },
    addEventListener(type, fn, capture = false) { this.listeners.push({ type, fn, capture: capture === true }); },
    all(sel) {
      const toks = sel.split(" ");
      const out = [];
      const visit = (n, anc) => {
        for (const k of n.kids) {
          if (typeof k === "string") continue;
          if (matches(k, toks.at(-1)) && toks.slice(0, -1).every((t) => anc.some((a) => matches(a, t)))) out.push(k);
          visit(k, [...anc, k]);
        }
      };
      visit(this, []);
      return out;
    },
    querySelectorAll(sel) { return this.all(sel); },
    querySelector(sel) { return this.all(sel)[0] ?? null; },
    // capture listeners of the target first, then its bubble listeners, then the ancestors'
    dispatch(type) {
      let stopped = false;
      const ev = { type, stopImmediatePropagation() { stopped = true; } };
      for (let n = this; n && !stopped; n = n.parent) {
        for (const l of n.listeners.filter((x) => x.type === type && (n === this ? x.capture : false)).concat(n.listeners.filter((x) => x.type === type && !x.capture))) {
          if (stopped) break;
          if (n === this || !l.capture) l.fn(ev);
        }
      }
    },
  };
  return e;
}
globalThis.document = { createElement: el };

const { contentExtras } = await import(new URL("builder/elementsettings.js", root));

const kinds = [
  { kind: "image", props: [{ key: "href" }, { key: "lightbox" }] },
  { kind: "button", props: [{ key: "href" }, { key: "target" }, { key: "rel" }], ia: true },
  { kind: "container", props: [{ key: "href" }, { key: "target" }, { key: "rel" }] },
  { kind: "text", props: [] },
];
function mount(node, { lightbox = false } = {}) {
  const doc = { nodes: [node] };
  const log = [];
  const past = [];
  const commit = (ops, opts) => {
    past.push(JSON.stringify(doc.nodes[0]));
    log.push({ ops, label: opts?.label });
    for (const o of ops) {
      const p = (doc.nodes[0].p ??= {});
      if (o.value === null) delete p[o.key];
      else p[o.key] = o.value;
    }
    return true;
  };
  const panel = el("div");
  panel.isConnected = true;
  const wrap = el("div");
  wrap.setAttribute("data-prop", "href");
  const input = el("input");
  wrap.append(input);
  panel.append(wrap);
  let lbInput = null;
  if (lightbox) {
    const lb = el("div");
    lb.setAttribute("data-prop", "lightbox");
    lbInput = el("input");
    lb.append(lbInput);
    panel.append(lb);
  }
  input.setAttribute("aria-describedby", "hint1");
  const own = [];
  input.addEventListener("input", () => { own.push(input.value); commit([{ op: "setProp", id: node.id, key: "href", value: input.value || null }], { label: "Edit href" }); });
  contentExtras(doc.nodes[0], panel, { api: { kinds, getDoc: () => doc, options: {} }, commit });
  const undo = () => { doc.nodes[0] = JSON.parse(past.pop()); };
  return { doc, log, panel, input, own, undo, lbInput, group: panel.querySelector('[role="group"]') };
}

test("mounting a node with link options commits nothing", () => {
  const m = mount({ id: "b", t: "button", p: { href: "/a", target: "_blank", rel: "nofollow" } });
  assert.equal(m.log.length, 0);
  assert.equal(m.group.hidden, false);
  assert.equal(m.panel.querySelector("[data-target]").checked, true);
  assert.equal(m.panel.querySelector('[data-rel="nofollow"]').checked, true);
  assert.equal(m.panel.querySelector('[data-rel="ugc"]').checked, false);
});

test("a bound href counts as a link: options show and are kept", () => {
  const m = mount({ id: "b", t: "button", p: { target: "_blank" }, b: { href: "item.url" } });
  assert.equal(m.log.length, 0);
  assert.equal(m.group.hidden, false);
});

test("an unlinked node hides the options and commits nothing", () => {
  const m = mount({ id: "b", t: "button", p: {} });
  assert.equal(m.log.length, 0);
  assert.equal(m.group.hidden, true);
});

test("emptying the Link clears href, target and rel in one commit; the field's own handler does not run", () => {
  const m = mount({ id: "b", t: "button", p: { href: "/a", target: "_blank", rel: "nofollow ugc" } });
  m.input.value = "";
  m.input.dispatch("input");
  assert.equal(m.log.length, 1);
  assert.deepEqual(m.log[0].ops.map((o) => [o.key, o.value]), [["href", null], ["target", null], ["rel", null]]);
  assert.deepEqual(m.own, []);
  assert.deepEqual(m.doc.nodes[0].p, {});
  assert.equal(m.group.hidden, true);
  assert.equal(m.panel.querySelector("[data-target]").checked, false);
  m.undo();
  assert.deepEqual(m.doc.nodes[0].p, { href: "/a", target: "_blank", rel: "nofollow ugc" });
});

test("emptying a div container's Link also clears its accessible name, one undo step", () => {
  const m = mount({ id: "c", t: "container", p: { href: "/a", aria: "Read the guide" } });
  m.input.value = "";
  m.input.dispatch("input");
  assert.equal(m.log.length, 1);
  assert.deepEqual(m.log[0].ops.map((o) => [o.key, o.value]), [["href", null], ["aria", null]]);
  assert.deepEqual(m.doc.nodes[0].p, {});
  m.undo();
  assert.deepEqual(m.doc.nodes[0].p, { href: "/a", aria: "Read the guide" });
});

test("after undo a redrawn panel shows the restored checkboxes and still commits nothing", () => {
  const m = mount({ id: "b", t: "button", p: { href: "/a", target: "_blank", rel: "sponsored" } });
  m.input.value = "";
  m.input.dispatch("input");
  m.undo();
  const again = mount(m.doc.nodes[0]);
  assert.equal(again.log.length, 0);
  assert.equal(again.group.hidden, false);
  assert.equal(again.panel.querySelector("[data-target]").checked, true);
  assert.equal(again.panel.querySelector('[data-rel="sponsored"]').checked, true);
});

test("emptying a Link that has no options is the field's own edit", () => {
  const m = mount({ id: "b", t: "button", p: { href: "/a" } });
  m.input.value = "";
  m.input.dispatch("input");
  assert.deepEqual(m.own, [""]);
  assert.equal(m.log.length, 1);
  assert.equal(m.log[0].label, "Edit href");
});

test("a tick is never reverted by painting, even before the store has applied it", () => {
  const m = mount({ id: "b", t: "button", p: { href: "/a" } });
  const box = m.panel.querySelector("[data-target]");
  box.checked = true;
  box.dispatch("change");
  assert.equal(box.checked, true);
});

test("a container link with a button inside is explained in the field and not stored", () => {
  const m = mount({ id: "c", t: "container", p: {}, c: [{ id: "b1", t: "button", p: { text: "Buy" } }] });
  m.input.value = "/x";
  m.input.dispatch("input");
  assert.deepEqual(m.own, [], "the field's own handler must not store the link");
  assert.equal(m.log.length, 0);
  assert.equal(m.input.attrs["aria-invalid"], "true");
  const err = m.panel.querySelector('[class="hm-bs-err"]');
  assert.match(err.kids[0], /interactive content: “Button · Buy” is inside/);
  assert.doesNotMatch(err.kids[0], /b1/);
  assert.match(m.input.attrs["aria-describedby"], /hint1 .*-e/);
  m.input.value = "";
  m.input.dispatch("input");
  assert.equal(m.input.attrs["aria-invalid"], undefined);
  assert.equal(m.input.attrs["aria-describedby"], "hint1");
});

test("a container link on a section tag is explained", () => {
  const m = mount({ id: "c", t: "container", p: { tag: "section" }, c: [] });
  m.input.value = "/x";
  m.input.dispatch("input");
  assert.equal(m.log.length, 0);
  assert.match(m.panel.querySelector('[class="hm-bs-err"]').kids[0], /div tag/);
});

test("an existing unsaveable container link shows its reason when selected, without committing", () => {
  const m = mount({ id: "c", t: "container", p: { href: "/x" }, c: [{ id: "b1", t: "button", p: {} }] });
  assert.equal(m.log.length, 0);
  assert.equal(m.input.attrs["aria-invalid"], "true");
});

test("a plain link on a container with text only is stored by the field", () => {
  const m = mount({ id: "c", t: "container", p: {}, c: [{ id: "t1", t: "text", p: { html: "<b>x</b>" } }] });
  m.input.value = "/x";
  m.input.dispatch("input");
  assert.deepEqual(m.own, ["/x"]);
});

test("a disabled Link or Lightbox is explained in visible text the control is described by", () => {
  const linked = mount({ id: "i", t: "image", p: { href: "/x" } }, { lightbox: true });
  const note = linked.panel.querySelector('[class="hm-muted"]');
  assert.match(note.kids[0], /cannot also open a lightbox/);
  assert.equal(linked.lbInput.disabled, true);
  assert.match(linked.lbInput.attrs["aria-describedby"], /-n$/);
  const boxed = mount({ id: "i", t: "image", p: { lightbox: true } }, { lightbox: true });
  assert.match(boxed.panel.querySelector('[class="hm-muted"]').kids[0], /cannot also be a link/);
  assert.equal(boxed.input.disabled, true);
  assert.match(boxed.input.attrs["aria-describedby"], /hint1 .*-n$/);
  const free = mount({ id: "i", t: "image", p: {} }, { lightbox: true });
  assert.equal(free.lbInput.attrs["aria-describedby"], undefined);
  assert.equal(free.panel.querySelector('[class="hm-muted"]').kids[0] ?? "", "");
});
