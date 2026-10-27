// The bridge is one inline ES5 script with no JS-engine test of its own. Its inline
// normaliser (clean/ser/build, between the hm:inline markers) is pure, so the block is
// sliced out and run here against a stub DOM.
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/internal/render/canvas_bridge.js", import.meta.url), "utf8");
const a = src.indexOf("/* hm:inline:start */");
const b = src.indexOf("/* hm:inline:end */");
assert.ok(a > 0 && b > a, "inline marker block");
const { clean, ser, build, inlineHrefOk, textOfTree, applyFormat, marksOfTree, linkOfTree } = new Function(
  src.slice(a, b) +
    "; return { clean: clean, ser: ser, build: build, inlineHrefOk: inlineHrefOk, textOfTree: textOfTree, applyFormat: applyFormat, marksOfTree: marksOfTree, linkOfTree: linkOfTree };",
)();

// ---- stub DOM: nodeType, nodeName, childNodes, getAttribute, nodeValue ----
const text = (v) => ({ nodeType: 3, nodeName: "#text", nodeValue: v, childNodes: [] });
const el = (name, attrs = {}, ...kids) => ({
  nodeType: 1, nodeName: name.toUpperCase(), childNodes: kids.map((k) => (typeof k === "string" ? text(k) : k)),
  getAttribute: (n) => (n in attrs ? attrs[n] : null),
});
const root = (...kids) => el("div", {}, ...kids);
const comment = { nodeType: 8, nodeName: "#comment", nodeValue: "x", childNodes: [] };
const out = (kind, ...kids) => ser(clean(root(...kids), kind));

test("marks: b/i/strike/del map to strong/em/s, others kept", () => {
  assert.equal(out("text", el("b", {}, "a"), el("i", {}, "b"), el("strike", {}, "c"), el("del", {}, "d")), "<strong>a</strong><em>b</em><s>c</s><s>d</s>");
  assert.equal(out("text", el("u", {}, "u"), el("code", {}, "c"), el("strong", {}, "s"), el("em", {}, "e"), el("s", {}, "x")), "<u>u</u><code>c</code><strong>s</strong><em>e</em><s>x</s>");
});

test("nested marks keep their nesting; empty marks vanish", () => {
  assert.equal(out("text", el("b", {}, "a", el("i", {}, "b"))), "<strong>a<em>b</em></strong>");
  assert.equal(out("text", el("b", {}, ""), el("i", {}, el("span", {}))), "");
});

test("attributes are dropped, text is escaped", () => {
  assert.equal(out("text", el("b", { class: "x", style: "color:red", onclick: "x()" }, "a<b>&")), "<strong>a&lt;b&gt;&amp;</strong>");
});

test("disallowed tags: dropped with content or unwrapped", () => {
  for (const t of ["script", "style", "template", "iframe", "object", "svg", "math", "img", "noscript", "textarea"]) {
    assert.equal(out("text", "a", el(t, {}, "evil"), "b"), "ab", t);
  }
  assert.equal(out("text", el("span", { style: "x" }, "a", el("font", {}, "b")), el("h1", {}, "c")), "ab<br>c");
  assert.equal(out("text", comment, "a"), "a");
});

test("links: safe href kept with attributes reduced; unsafe unwrapped", () => {
  assert.equal(out("text", el("a", { href: "https://x.test/a?b=1&c=2", class: "k", onclick: "y" }, "go")), '<a href="https://x.test/a?b=1&amp;c=2">go</a>');
  assert.equal(out("text", el("a", { href: "/x", target: "_blank", rel: "opener" }, "go")), '<a href="/x" target="_blank" rel="noopener noreferrer">go</a>');
  assert.equal(out("text", el("a", { href: "/x", target: "_top" }, "go")), '<a href="/x">go</a>');
  for (const bad of ["javascript:alert(1)", " JaVaScRiPt:alert(1)", "data:text/html,x", "vbscript:x", "//evil.test", "/\\evil.test", "", null]) {
    assert.equal(out("text", el("a", bad === null ? {} : { href: bad }, el("b", {}, "go"))), "<strong>go</strong>", String(bad));
  }
});

test("quotes in an href are percent-encoded", () => {
  assert.equal(out("text", el("a", { href: '/x"onmouseover="y' }, "go")), '<a href="/x%22onmouseover=%22y">go</a>');
});

test("br: kept in a text, a space in a heading, trailing ones trimmed", () => {
  assert.equal(out("text", "a", el("br"), "b", el("br"), el("br")), "a<br>b");
  assert.equal(out("heading", "a", el("br"), "b"), "a b");
  assert.equal(out("heading", "a", el("br")), "a");
});

test("blocks (paste from Word / Google Docs / contenteditable divs)", () => {
  assert.equal(out("text", "a", el("div", {}, "b"), el("div", {}, "c")), "a<br>b<br>c");
  assert.equal(out("text", el("p", { class: "MsoNormal" }, el("span", { style: "mso-x" }, "one")), el("p", {}, "two")), "one<br>two");
  assert.equal(out("heading", el("p", {}, "one"), el("p", {}, "two")), "one two");
  assert.equal(out("text", el("meta"), el("div", {}, el("b", { id: "docs-internal-guid" }, "x", el("span", {}, " y")))), "<strong>x y</strong>");
  assert.equal(out("text", "a", el("div", {}, el("br")), "b"), "a<br><br>b"); // a blank line stays one
});

test("newlines in text nodes are whitespace; nbsp is kept", () => {
  assert.equal(out("text", "a\nb\r\nc"), "a b c");
  assert.equal(out("text", "a\u00a0b"), "a\u00a0b");
});

test("plain content has no items; marked content does", () => {
  const plain = clean(root("hello ", "world"), "text");
  assert.ok(plain.every((x) => typeof x === "string"));
  assert.equal(textOfTree(plain), "hello world");
  assert.equal(textOfTree(clean(root(el("b", {}, "a"), "b"), "text")), "ab");
});

test("ser(clean(x)) is idempotent through a re-read of its own output", () => {
  // a tiny reader for the canonical output: <tag>..</tag>, <br>, <a href=..>, text
  const parse = (s) => {
    const stack = [{ kids: [] }];
    const rx = /<(\/?)([a-z]+)((?: [a-z]+="[^"]*")*)>|([^<]+)/g;
    let m;
    const unesc = (v) => v.replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
    while ((m = rx.exec(s))) {
      if (m[4] !== undefined) stack.at(-1).kids.push(text(unesc(m[4])));
      else if (m[1]) stack.pop();
      else {
        const attrs = {};
        for (const x of m[3].matchAll(/ ([a-z]+)="([^"]*)"/g)) attrs[x[1]] = unesc(x[2]);
        const node = el(m[2], attrs);
        stack.at(-1).kids.push(node);
        if (m[2] !== "br") stack.push(node);
        node.kids = node.childNodes;
      }
    }
    return root(...stack[0].kids);
  };
  const cases = [
    [el("b", {}, "a ", el("i", {}, "b")), " & ", el("a", { href: "/x?y=1&z=2", target: "_blank" }, "l<>")],
    ["x", el("br"), el("code", {}, "y"), el("br"), "z"],
  ];
  for (const kids of cases) {
    const once = out("text", ...kids);
    assert.equal(ser(clean(parse(once), "text")), once);
  }
});

test("build makes DOM nodes for the tree", () => {
  const made = [];
  const d = {
    createDocumentFragment: () => ({ kids: [], appendChild(c) { this.kids.push(c); } }),
    createTextNode: (v) => ({ text: v }),
    createElement: (n) => { const e = { name: n, attrs: {}, kids: [], setAttribute(k, v) { this.attrs[k] = v; }, appendChild(f) { this.kids.push(...(f.kids || [f])); } }; made.push(e); return e; },
  };
  const frag = build(clean(root("a", el("a", { href: "/x", target: "_blank" }, el("b", {}, "b")), el("br")), "text"), d);
  assert.equal(frag.kids[0].text, "a");
  assert.deepEqual(frag.kids[1].attrs, { href: "/x", target: "_blank", rel: "noopener noreferrer" });
  assert.equal(frag.kids[1].kids[0].name, "strong");
  assert.equal(frag.kids.length, 2);
});

test("href rule agrees with the shared fixture", () => {
  const cases = JSON.parse(readFileSync(new URL("../../../backend/internal/entity/testdata/inline_href_cases.json", import.meta.url), "utf8"));
  for (const c of cases) assert.equal(inlineHrefOk(c.href), c.ok, JSON.stringify(c.href));
});

// ---- formatting: marks applied to a range of plain-text offsets ----
const tree = (...kids) => clean(root(...kids), "text");
const fmt = (kids, start, end, cmd, href, blank) => ser(applyFormat(tree(...kids), start, end, cmd, href, blank));

test("bold wraps the range, splitting text around it", () => {
  assert.equal(fmt(["hello world"], 6, 11, "bold"), "hello <strong>world</strong>");
  assert.equal(fmt(["hello world"], 0, 5, "italic"), "<em>hello</em> world");
  assert.equal(fmt(["abc"], 1, 2, "code"), "a<code>b</code>c");
  assert.equal(fmt(["abc"], 0, 3, "underline"), "<u>abc</u>");
  assert.equal(fmt(["abc"], 0, 3, "strike"), "<s>abc</s>");
});

test("bold toggles off when every character in the range is bold", () => {
  assert.equal(fmt([el("b", {}, "hello world")], 6, 11, "bold"), "<strong>hello </strong>world");
  assert.equal(fmt([el("b", {}, "hello world")], 0, 11, "bold"), "hello world");
  assert.equal(fmt([el("b", {}, "ab"), el("strong", {}, "cd")], 1, 3, "bold"), "<strong>a</strong>bc<strong>d</strong>");
});

test("a range that is only partly bold becomes all bold", () => {
  assert.equal(fmt(["ab", el("b", {}, "cd"), "ef"], 1, 5, "bold"), "a<strong>bcde</strong>f");
});

test("marks nest in a fixed order, whatever the order they were added in", () => {
  const once = ser(applyFormat(applyFormat(tree("abc"), 0, 3, "italic"), 0, 3, "bold"));
  assert.equal(once, "<strong><em>abc</em></strong>");
  assert.equal(ser(applyFormat(tree(el("em", {}, "abc")), 0, 3, "link", "/x", false)), '<a href="/x"><em>abc</em></a>');
});

test("link sets, replaces and removes links", () => {
  assert.equal(fmt(["go here now"], 3, 7, "link", "https://a.test/x", true), 'go <a href="https://a.test/x" target="_blank" rel="noopener noreferrer">here</a> now');
  assert.equal(fmt([el("a", { href: "/old" }, "link")], 0, 4, "link", "/new", false), '<a href="/new">link</a>');
  assert.equal(fmt(["a ", el("a", { href: "/x" }, "link"), " b"], 3, 5, "unlink"), 'a <a href="/x">l</a>in<a href="/x">k</a> b');
});

test("a caret inside a link links/unlinks the whole link", () => {
  const kids = ["a ", el("a", { href: "/x" }, "lin", el("b", {}, "k")), " b"];
  assert.equal(fmt(kids, 4, 4, "unlink"), "a lin<strong>k</strong> b");
  assert.equal(fmt(kids, 4, 4, "link", "/y", false), 'a <a href="/y">lin<strong>k</strong></a> b');
  assert.equal(fmt(["plain"], 2, 2, "link", "/y", false), "plain"); // no link here, nothing selected: nothing to do
  assert.equal(fmt(["plain"], 2, 2, "bold"), "plain");
});

test("clear removes every mark in the range, links included", () => {
  assert.equal(fmt([el("b", {}, "a", el("i", {}, "b")), el("a", { href: "/x" }, "c")], 0, 3, "clear"), "abc");
  assert.equal(fmt([el("b", {}, "abc")], 1, 2, "clear"), "<strong>a</strong>b<strong>c</strong>");
});

test("a br stays where it was, outside the marks it was not in", () => {
  assert.equal(fmt(["ab", el("br"), "cd"], 1, 3, "bold"), "a<strong>b</strong><br><strong>c</strong>d");
});

test("formatting an empty range or empty text changes nothing", () => {
  assert.equal(fmt(["abc"], 1, 1, "bold"), "abc");
  assert.equal(fmt([], 0, 0, "bold"), "");
});

test("marksOfTree: the marks of every character in the range, or around the caret", () => {
  const t = tree("ab", el("b", {}, "cd"), el("a", { href: "/x" }, el("i", {}, "ef")));
  assert.deepEqual(marksOfTree(t, 2, 4), ["bold"]);
  assert.deepEqual(marksOfTree(t, 1, 4), []);
  assert.deepEqual(marksOfTree(t, 4, 6), ["italic", "link"]);
  assert.deepEqual(marksOfTree(t, 3, 3), ["bold"]);
  assert.deepEqual(marksOfTree(t, 2, 2), []); // caret at the start of the bold: typing continues the plain text before it
  assert.deepEqual(marksOfTree(t, 4, 4), ["bold"]);
  assert.deepEqual(marksOfTree(t, 5, 5), ["italic", "link"]);
  assert.deepEqual(marksOfTree(tree(), 0, 0), []);
  const all = tree(el("a", { href: "/x" }, el("code", {}, el("s", {}, el("u", {}, el("i", {}, el("b", {}, "x")))))));
  assert.deepEqual(marksOfTree(all, 0, 1), ["bold", "italic", "underline", "strike", "code", "link"]);
});

test("formatted output always stays inside the allowed subset", () => {
  const out = fmt([el("span", { style: "x" }, "a"), el("b", {}, "b")], 0, 2, "italic");
  assert.equal(out, "<em>a</em><strong><em>b</em></strong>");
});

test("ser writes the canonical form shared with Go (backend/testdata/inline/canonical.json)", () => {
  const cases = JSON.parse(readFileSync(new URL("../../../backend/testdata/inline/canonical.json", import.meta.url), "utf8"));
  for (const c of cases.filter((c) => c.tree)) assert.equal(ser(c.tree), c.canonical, c.name);
});

test("linkOfTree: the address and target of the link under the selection, null when part of it is outside", () => {
  const t = clean(root("ab ", el("a", { href: "/x", target: "_blank" }, "cd", el("b", {}, "ef")), " gh ", el("a", { href: "/y" }, "ij")), "text");
  assert.deepEqual(linkOfTree(t, 3, 5), { href: "/x", blank: true });
  assert.deepEqual(linkOfTree(t, 4, 4), { href: "/x", blank: true });
  assert.deepEqual(linkOfTree(t, 3, 7), { href: "/x", blank: true }); // across the bold inside the same link
  assert.equal(linkOfTree(t, 0, 5), null); // starts outside
  assert.equal(linkOfTree(t, 3, 12), null); // two different links
  assert.deepEqual(linkOfTree(t, 12, 13), { href: "/y", blank: false });
  assert.equal(linkOfTree(t, 8, 9), null);
});

// The frame-side keydown that starts an edit on Enter also sees the Enter that ended a button-label edit (the in-edit
// listener prevented it and blurred): it must leave a handled key alone, or the edit starts again at once.
test("the Enter-to-edit keydown ignores a key an edit handler already used", () => {
  const i = src.indexOf("// Enter on the selected element starts editing");
  assert.ok(i > 0, "the Enter-to-edit handler");
  const head = src.slice(src.lastIndexOf("doc.addEventListener('keydown'", i), i);
  assert.match(head, /e\.defaultPrevented/);
});

// The heading formatting bar has no underline, strike or code, so Ctrl+U and the OS menu (which both end in fmt) must not apply them.
test("fmt refuses underline, strike and code on a heading", () => {
  const i = src.indexOf("function fmt(cmd, href, newTab)");
  assert.ok(i > 0, "fmt");
  assert.match(src.slice(i, i + 400), /rk === 'heading' && \/\^\(underline\|strike\|code\)\$\/\.test\(cmd\)/);
});

// Focusing a link (a button label) leaves no caret in it, so typing went nowhere: startProp puts one at the end when the
// double-click or the keyboard did not (the browser check in builder-inline.mjs types into a button label).
test("startProp puts a caret in the element when focus left none", () => {
  const i = src.indexOf("function startProp(el)");
  assert.match(src.slice(i, i + 900), /if \(!rangeIn\(el\)\)[\s\S]*collapse\(false\)/);
});
