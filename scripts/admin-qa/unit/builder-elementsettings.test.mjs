// The element settings chunk's pure helpers: the link datalist (pages + #anchors), rel order, anchors of a doc.
// Run: node --test scripts/admin-qa/unit/builder-elementsettings.test.mjs
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
const { controlOf, needsAlt } = await import(new URL("../../../backend/web/admin/js/builder/inspcontent.js", import.meta.url));
const { anchorsOf, linkChoices, joinRel, anchorProblem, linkProblem, interactiveIn, lightboxUnnamed, lightboxWarning } = await import(new URL("../../../backend/web/admin/js/builder/elementsettings.js", import.meta.url));

const doc = { v: 2, nodes: [{ id: "a", t: "container", p: { anchor: "pricing" }, c: [{ id: "b", t: "button", p: { anchor: "buy", href: "#pricing" } }, { id: "c", t: "text", p: { text: "x" } }] }, { id: "d", t: "container", p: { anchor: "pricing" } }] };

test("anchorsOf walks every depth and skips nodes without one", () => {
  assert.deepEqual(anchorsOf(doc), ["pricing", "buy", "pricing"]);
  assert.deepEqual(anchorsOf({ nodes: [] }), []);
});

test("linkChoices lists pages first, then the page's anchors once, sorted", () => {
  assert.deepEqual(linkChoices(doc, ["/about", "/pricing"]), ["/about", "/pricing", "#buy", "#pricing"]);
  assert.deepEqual(linkChoices({ nodes: [] }), []);
});

test("joinRel keeps the fixed token order and drops everything else", () => {
  assert.equal(joinRel(["ugc", "nofollow"]), "nofollow ugc");
  assert.equal(joinRel(["sponsored", "nofollow", "ugc"]), "nofollow sponsored ugc");
  assert.equal(joinRel(["noopener"]), "");
  assert.equal(joinRel([]), "");
});

test("anchorProblem mirrors the server: grammar, reserved names, node ids, duplicates", () => {
  const d = { nodes: [{ id: "a", t: "container", p: { anchor: "pricing" }, c: [{ id: "n0b73", t: "text" }, { id: "me", t: "text" }] }] };
  const me = d.nodes[0].c[1];
  const reserved = ["main-content", "toc", "top", "main", "content"];
  assert.equal(anchorProblem("", me, d, reserved), "");
  assert.equal(anchorProblem("fine-one", me, d, reserved), "");
  assert.match(anchorProblem("1bad", me, d, reserved), /Start with a letter/);
  assert.match(anchorProblem("Upper", me, d, reserved), /Start with a letter/);
  for (const r of ["main-content", "top", "content", "hm-x", "intro-strip-label"]) assert.match(anchorProblem(r, me, d, reserved), /used by the page itself/, r);
  assert.match(anchorProblem("n0b73", me, d, reserved), /id of an element/); // another node's id
  assert.match(anchorProblem("me", me, d, reserved), /id of an element/); // its own id too, as the server
  assert.match(anchorProblem("pricing", me, d, reserved), /already used/);
  assert.equal(anchorProblem("pricing", d.nodes[0], d, reserved), ""); // its own anchor is not a duplicate
});

const kinds = { button: { ia: true }, menu: { ia: true }, text: {}, container: {}, image: {}, gallery: {}, heading: {} };
test("interactiveIn finds what the server calls interactive, depth first", () => {
  const t = (c) => ({ id: "c", t: "container", p: {}, c });
  assert.equal(interactiveIn(t([{ id: "a", t: "text", p: { html: "plain" } }]), kinds), null);
  assert.equal(interactiveIn(t([{ id: "a", t: "container", c: [{ id: "b", t: "button" }] }]), kinds).id, "b");
  assert.equal(interactiveIn(t([{ id: "a", t: "text", p: { html: 'see <a href="/x">this</a>' } }]), kinds).id, "a");
  assert.equal(interactiveIn(t([{ id: "i", t: "image", p: { href: "/x" } }]), kinds).id, "i");
  assert.equal(interactiveIn(t([{ id: "i", t: "image", p: {}, b: { href: "item.url" } }]), kinds).id, "i");
  assert.equal(interactiveIn(t([{ id: "i", t: "image", p: { lightbox: true } }]), kinds).id, "i");
  assert.equal(interactiveIn(t([{ id: "i", t: "image", p: {} }]), kinds), null);
  assert.equal(interactiveIn(t([{ id: "g", t: "gallery", p: { lightbox: true } }]), kinds).id, "g");
  assert.equal(interactiveIn(t([{ id: "k", t: "container", p: { href: "/y" } }]), kinds).id, "k");
  assert.equal(interactiveIn(t([{ id: "m", t: "menu" }]), kinds).id, "m");
});

test("linkProblem names the cause, not a node id", () => {
  assert.equal(linkProblem({ id: "x", t: "button" }, kinds), "");
  assert.match(linkProblem({ id: "x", t: "container", p: { tag: "section" }, c: [] }, kinds), /div tag/);
  assert.match(linkProblem({ id: "x", t: "container", p: { tag: "div" }, c: [{ id: "b9", t: "button", p: { text: "Go" } }] }, kinds), /“Button · Go”/);
  assert.equal(linkProblem({ id: "x", t: "container", p: {}, c: [{ id: "t", t: "text", p: {} }] }, kinds), "");
});

test("a lightbox image needs a name: aria, alt (not when decorative) or a caption", () => {
  const img = (p, b) => ({ id: "i", t: "image", p: { src: "/a.jpg", ...p }, b });
  assert.equal(lightboxUnnamed(img({ lightbox: true })), true);
  assert.equal(lightboxUnnamed(img({ lightbox: true, alt: "  " })), true);
  assert.equal(lightboxUnnamed(img({ lightbox: true, alt: "Cat" })), false);
  assert.equal(lightboxUnnamed(img({ lightbox: true, decorative: true, alt: "Cat" })), true);
  assert.equal(lightboxUnnamed(img({ lightbox: true, decorative: true, caption: "Cat" })), false);
  assert.equal(lightboxUnnamed(img({ lightbox: true, aria: "Open" })), false);
  assert.equal(lightboxUnnamed(img({ lightbox: true }, { alt: "item.title" })), false);
  assert.equal(lightboxUnnamed(img({})), false, "no lightbox, no need");
  assert.equal(lightboxUnnamed(img({ lightbox: true, href: "/x" })), false, "a link has its own rules");
  assert.equal(lightboxUnnamed(img({}), { t: "gallery", p: { lightbox: true } }), true, "through its gallery");
  assert.equal(lightboxUnnamed(img({}), { t: "gallery", p: {} }), false);
});

test("lightboxWarning says what to add, and counts a gallery's unnamed images", () => {
  const img = (id, p) => ({ id, t: "image", p: { src: "/a.jpg", ...p } });
  assert.match(lightboxWarning(img("a", { lightbox: true })), /alt text or a caption/);
  assert.match(lightboxWarning(img("a", { lightbox: true, decorative: true })), /decorative.*caption/);
  assert.equal(lightboxWarning(img("a", { lightbox: true, alt: "x" })), "");
  const g = { id: "g", t: "gallery", p: { lightbox: true }, c: [img("a", {}), img("b", { alt: "x" }), img("c", {})] };
  assert.match(lightboxWarning(g), /^2 images in this gallery open in a lightbox/);
  assert.equal(lightboxWarning({ ...g, p: {} }), "");
  assert.match(lightboxWarning({ ...g, c: [img("a", {})] }), /^1 image in this gallery opens/);
});

test("a plugin widget's own anchor prop is not a page anchor", () => {
  const kinds = [{ kind: "container", u: ["anchor", "aria"] }, { kind: "pack", u: ["aria"] }];
  const d = { nodes: [{ id: "a", t: "container", p: { anchor: "top-x" } }, { id: "b", t: "pack", p: { anchor: "mine" } }] };
  assert.deepEqual(anchorsOf(d, kinds), ["top-x"]);
  assert.deepEqual(anchorsOf(d), ["top-x", "mine"]);
  assert.deepEqual(linkChoices(d, [], kinds), ["#top-x"]);
  assert.equal(anchorProblem("mine", d.nodes[0], d, [], kinds), "");
  assert.match(anchorProblem("mine", d.nodes[0], d, [], null), /already used/);
});
