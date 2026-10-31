// postMessage protocols between the canvas iframe bridge
// (backend/internal/render/canvas_bridge.js) and the admin parent: v1 (hm: 1,
// section-level) and v2 (hm: 2, any node, patching). docs/CANVAS.md lists every
// message and rule; testdata/canvas_protocol_cases.json pins them.
// Every inbound payload is untrusted: parseFrameMessage validates shape only;
// callers still verify section ids and paths against the form.

/** Contract version the parent accepts from <html data-hm-contract>. */
export const CANVAS_CONTRACT = "1";
/** Bounds for hm:edit-text payloads. */
export const MAX_TEXT_INDEX = 1_000_000;
export const MAX_TEXT_LEN = 200_000;
/** The longest `text`/`html` a v2 text message may carry: the schema's maxLength, so an autosave never fails on length. */
export const MAX_NODE_TEXT = 20_000;
/** Contract of the v2 message family (the bridge's `ready`). */
export const CANVAS_CONTRACT_V2 = "2";
/** v2 limits. Ids are the builder grammar; nothing else reaches a selector. */
export const MAX_NODES = 5000;
export const MAX_ID = 64;
export const ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
export const MAX_HTML = 1_000_000;
export const MAX_CSS_ENTRIES = 200;
export const MAX_CSS_RULE = 100_000;
export const MAX_DECLS = 50;
export const MAX_DECL_VALUE = 200;
const PROP_RE = /^[a-z-]{1,40}$/;
const TEXT_PROP_RE = /^[A-Za-z0-9_-]{1,40}$/;
/** Inline marks the bridge reports in `selection`, and the commands of `format`. */
export const MARKS = ["bold", "italic", "underline", "strike", "code", "link"];
export const FORMAT_CMDS = [...MARKS, "unlink", "clear", "done"];
export const MAX_HREF = 2000;

const isObj = (v) => typeof v === "object" && v !== null && !Array.isArray(v);
const isStr = (v) => typeof v === "string";
const isNum = (v) => typeof v === "number" && Number.isFinite(v);
const isBox = (v) => isObj(v) && isNum(v.top) && isNum(v.height);
const isRect = (v) => isBox(v) && isNum(v.left) && isNum(v.width);
const isLayoutSection = (v) => isObj(v) && isStr(v.id) && isStr(v.type) && isNum(v.top) && isNum(v.height);

const isId = (v) => isStr(v) && ID_RE.test(v);
const isKind = (v) => v === "s" || v === "n";
const isRect4 = (v) => isObj(v) && isNum(v.x) && isNum(v.y) && isNum(v.w) && isNum(v.h);
const isPd = (v) => Array.isArray(v) && v.length === 4 && v.every((n) => Number.isInteger(n) && n >= 0 && n <= 10000);
const isLayoutNode = (v) =>
  isObj(v) && isId(v.id) && isKind(v.k) && isStr(v.t) && v.t.length <= 100 && (v.p === "" || isId(v.p)) &&
  isNum(v.x) && isNum(v.y) && isNum(v.w) && isNum(v.h) &&
  (v.c === undefined || v.c === 1) && (v.dir === undefined || v.dir === "row" || v.dir === "column" || v.dir === "grid") &&
  (v.pd === undefined || isPd(v.pd));

function layoutNode(v) {
  const o = { id: v.id, k: v.k, t: v.t, p: v.p, x: v.x, y: v.y, w: v.w, h: v.h };
  if (v.c !== undefined) o.c = v.c;
  if (v.dir !== undefined) o.dir = v.dir;
  if (v.pd !== undefined) o.pd = v.pd.slice();
  return o;
}

/** Strict guard for the v2 family (bridge -> parent): the typed message or null. */
function parseV2(data) {
  switch (data.type) {
    case "ready":
      return isStr(data.contract) ? { hm: 2, type: "ready", contract: data.contract } : null;
    case "layout":
      if (!isNum(data.docH) || !isRect4(data.main) || !Array.isArray(data.nodes)) return null;
      if (data.nodes.length > MAX_NODES || !data.nodes.every(isLayoutNode)) return null;
      return {
        hm: 2, type: "layout", docH: data.docH, nodes: data.nodes.map(layoutNode),
        main: { x: data.main.x, y: data.main.y, w: data.main.w, h: data.main.h },
      };
    case "hover":
      if (data.id === null) return { hm: 2, type: "hover", id: null };
      return isId(data.id) && isKind(data.k) ? { hm: 2, type: "hover", id: data.id, k: data.k } : null;
    case "select":
      return isId(data.id) && isKind(data.k) && typeof data.shift === "boolean" && typeof data.ctrl === "boolean"
        ? { hm: 2, type: "select", id: data.id, k: data.k, shift: data.shift, ctrl: data.ctrl }
        : null;
    case "contextmenu":
      return isId(data.id) && isKind(data.k) && isNum(data.x) && isNum(data.y)
        ? { hm: 2, type: "contextmenu", id: data.id, k: data.k, x: data.x, y: data.y }
        : null;
    case "text": {
      // the html prop carries `html`, every other prop `value` (never both)
      if (!isId(data.id) || !isKind(data.k) || !isStr(data.prop) || !TEXT_PROP_RE.test(data.prop)) return null;
      const key = data.prop === "html" ? "html" : "value";
      const other = key === "html" ? "value" : "html";
      return isStr(data[key]) && data[key].length <= MAX_NODE_TEXT && data[other] === undefined
        ? { hm: 2, type: "text", id: data.id, k: data.k, prop: data.prop, [key]: data[key] }
        : null;
    }
    case "selection":
      return isId(data.id) && isKind(data.k) && isStr(data.prop) && TEXT_PROP_RE.test(data.prop) && isRect4(data.rect) &&
        Array.isArray(data.marks) && data.marks.length <= MARKS.length &&
        data.marks.every((m, i) => MARKS.includes(m) && data.marks.indexOf(m) === i) &&
        (data.el === undefined || isRect4(data.el)) && (data.kind === undefined || data.kind === "heading" || data.kind === "text") && (data.link === undefined || (isObj(data.link) && isStr(data.link.href) && data.link.href.length <= MAX_HREF && typeof data.link.blank === "boolean"))
        ? {
          hm: 2, type: "selection", id: data.id, k: data.k, prop: data.prop,
          rect: { x: data.rect.x, y: data.rect.y, w: data.rect.w, h: data.rect.h }, marks: [...data.marks],
          // el: the edited element's box (the bar goes by it); link: the address and target of the link under the selection
          ...(data.el && { el: { x: data.el.x, y: data.el.y, w: data.el.w, h: data.el.h } }),
          ...(data.kind && { kind: data.kind }), // heading or text: the bar offers B, I, Link and Clear on a heading
          ...(data.link && { link: { href: data.link.href, blank: data.link.blank } }),
        }
        : null;
    case "textend":
      return isId(data.id) && isKind(data.k) && isStr(data.prop) && TEXT_PROP_RE.test(data.prop)
        ? { hm: 2, type: "textend", id: data.id, k: data.k, prop: data.prop }
        : null;
    case "textlimit":
      return isId(data.id) && isKind(data.k) && isStr(data.prop) && TEXT_PROP_RE.test(data.prop)
        ? { hm: 2, type: "textlimit", id: data.id, k: data.k, prop: data.prop }
        : null;
    case "painted":
      // ok is false when the frame could not apply the patch; absent means true
      return Number.isInteger(data.seq) && data.seq >= 0 && (data.ok === undefined || typeof data.ok === "boolean")
        ? { hm: 2, type: "painted", seq: data.seq, ok: data.ok !== false }
        : null;
    case "key":
      return isStr(data.key) && data.key.length >= 1 && data.key.length <= 20 && typeof data.shift === "boolean"
        ? { hm: 2, type: "key", key: data.key, shift: data.shift }
        : null;
    default:
      return null;
  }
}

/** Strict runtime guard: the typed message, or null for anything else. */
export function parseFrameMessage(data) {
  if (!isObj(data) || !isStr(data.type)) return null;
  if (data.hm === 2) return parseV2(data);
  if (data.hm !== 1) return null;
  switch (data.type) {
    case "hm:ready":
      return isStr(data.contract) ? { hm: 1, type: "hm:ready", contract: data.contract } : null;
    case "hm:layout": {
      if (!isNum(data.docHeight) || !Array.isArray(data.sections)) return null;
      if (!data.sections.every(isLayoutSection)) return null;
      if (data.header !== undefined && !isBox(data.header)) return null;
      if (data.footer !== undefined && !isBox(data.footer)) return null;
      return {
        hm: 1, type: "hm:layout", docHeight: data.docHeight,
        sections: data.sections.map((s) => ({ id: s.id, type: s.type, top: s.top, height: s.height })),
        header: data.header, footer: data.footer,
      };
    }
    case "hm:select":
      if (!isStr(data.sectionId)) return null;
      return isStr(data.path)
        ? { hm: 1, type: "hm:select", sectionId: data.sectionId, path: data.path }
        : { hm: 1, type: "hm:select", sectionId: data.sectionId };
    case "hm:edit":
      return isStr(data.sectionId) && isStr(data.path) && isStr(data.value) && data.kind === "plaintext"
        ? { hm: 1, type: "hm:edit", sectionId: data.sectionId, path: data.path, value: data.value, kind: "plaintext" }
        : null;
    case "hm:edit-text":
      return isStr(data.sectionId) && isStr(data.path) && isStr(data.value) && isStr(data.expect) &&
        Number.isInteger(data.index) && data.index >= 0 && data.index <= MAX_TEXT_INDEX &&
        data.value.length <= MAX_TEXT_LEN && data.expect.length <= MAX_TEXT_LEN
        ? { hm: 1, type: "hm:edit-text", sectionId: data.sectionId, path: data.path, index: data.index, value: data.value, expect: data.expect }
        : null;
    case "hm:rich-open":
      return isStr(data.sectionId) && isStr(data.path) && isRect(data.rect)
        ? { hm: 1, type: "hm:rich-open", sectionId: data.sectionId, path: data.path, rect: data.rect }
        : null;
    case "hm:chrome-click":
      return data.area === "header" || data.area === "footer" ? { hm: 1, type: "hm:chrome-click", area: data.area } : null;
    case "hm:scroll":
      return isNum(data.y) ? { hm: 1, type: "hm:scroll", y: data.y } : null;
    case "hm:save":
      return { hm: 1, type: "hm:save" };
    case "hm:details-open":
      return Array.isArray(data.fields) && data.fields.every((f) => isObj(f) && isStr(f.sectionId) && isStr(f.path))
        ? { hm: 1, type: "hm:details-open", fields: data.fields.map((f) => ({ sectionId: f.sectionId, path: f.path })) }
        : null;
    default:
      return null;
  }
}

const isNodeRef = (sel, key) => (sel === "section" || sel === "node") && isId(key);
const isPlace = (v) => isObj(v) && (v.sel === "main" ? true : isNodeRef(v.sel, v.key));
const placements = (m) => ["before", "after", "parent"].filter((k) => m[k] !== undefined);
const okSeq = (m) => m.seq === undefined || (Number.isInteger(m.seq) && m.seq >= 0);
const STATES = ["hover", "focus", "active", "parent-hover"];
const okHtml = (m) => isStr(m.html) && m.html.length <= MAX_HTML;
const okCssMap = (c) =>
  isObj(c) && Object.keys(c).length <= MAX_CSS_ENTRIES &&
  Object.entries(c).every(([k, v]) => isId(k) && isStr(v) && v.length <= MAX_CSS_RULE);
const okPlace = (m) => {
  const p = placements(m);
  return p.length === 1 && isPlace(m[p[0]]);
};
const okDecls = (d) =>
  Array.isArray(d) && d.length <= MAX_DECLS &&
  d.every((x) => Array.isArray(x) && x.length === 2 && isStr(x[0]) && PROP_RE.test(x[0]) &&
    (x[1] === null || (isStr(x[1]) && x[1].length <= MAX_DECL_VALUE)));

// href is required for link only; where given it is a string of 1..MAX_HREF characters.
const okFormatArgs = (m) =>
  (m.href === undefined ? m.cmd !== "link" : isStr(m.href) && m.href.length >= 1 && m.href.length <= MAX_HREF) &&
  (m.newTab === undefined || typeof m.newTab === "boolean");

/** True when msg is a well-formed parent -> bridge v2 message within the limits.
 * The bridge applies the same rules to what it receives. */
export function validToBridge(msg) {
  if (!isObj(msg) || msg.hm !== 2 || !isStr(msg.type) || !okSeq(msg)) return false;
  switch (msg.type) {
    case "init":
      return typeof msg.editable === "boolean" && (msg.selectedKey === null || isId(msg.selectedKey));
    case "patch":
      return isNodeRef(msg.sel, msg.key) && okHtml(msg) && (msg.css === undefined || okCssMap(msg.css));
    case "insert":
      return okHtml(msg) && (msg.css === undefined || okCssMap(msg.css)) && okPlace(msg);
    case "move":
      return isNodeRef(msg.sel, msg.key) && okPlace(msg);
    case "remove":
    case "edit":
      return isNodeRef(msg.sel, msg.key);
    case "setVar":
      return isNodeRef(msg.sel, msg.key) && okDecls(msg.decls);
    case "css":
      return isNodeRef(msg.sel, msg.key) && okCssMap(msg.css);
    case "select":
      return isNodeRef(msg.sel, msg.key) && typeof msg.scrollIntoView === "boolean";
    case "scrollTo":
      return isNum(msg.y);
    case "format":
      return isNodeRef(msg.sel, msg.key) && FORMAT_CMDS.includes(msg.cmd) && okFormatArgs(msg);
    case "force":
      return isNodeRef(msg.sel, msg.key) && (msg.state === null || STATES.includes(msg.state));
    default:
      return false;
  }
}

const withSeq = (m, o) => (o.seq === undefined ? m : { ...m, seq: o.seq });
const place = (o) => {
  const p = {};
  for (const k of ["before", "after", "parent"]) if (o[k] !== undefined) p[k] = o[k];
  return p;
};

/** Builders for every parent -> bridge v2 message, so shapes live in one place.
 * Validate with validToBridge before posting. */
export const toBridge = {
  init: (o) => ({ hm: 2, type: "init", editable: o.editable !== false, selectedKey: o.selectedKey ?? null }),
  patch: (o) => withSeq({ hm: 2, type: "patch", sel: o.sel, key: o.key, html: o.html, ...(o.css ? { css: o.css } : {}) }, o),
  insert: (o) => withSeq({ hm: 2, type: "insert", html: o.html, ...(o.css ? { css: o.css } : {}), ...place(o) }, o),
  move: (o) => withSeq({ hm: 2, type: "move", sel: o.sel, key: o.key, ...place(o) }, o),
  remove: (o) => withSeq({ hm: 2, type: "remove", sel: o.sel, key: o.key }, o),
  setVar: (o) => withSeq({ hm: 2, type: "setVar", sel: o.sel, key: o.key, decls: o.decls }, o),
  css: (o) => withSeq({ hm: 2, type: "css", sel: o.sel, key: o.key, css: o.css }, o),
  edit: (o) => withSeq({ hm: 2, type: "edit", sel: o.sel, key: o.key }, o),
  select: (o) => withSeq({ hm: 2, type: "select", sel: o.sel, key: o.key, scrollIntoView: o.scrollIntoView === true }, o),
  force: (o) => withSeq({ hm: 2, type: "force", sel: o.sel, key: o.key, state: o.state ?? null }, o),
  format: (o) =>
    withSeq({ hm: 2, type: "format", sel: o.sel, key: o.key, cmd: o.cmd, ...(o.href !== undefined ? { href: o.href } : {}), ...(o.newTab !== undefined ? { newTab: o.newTab } : {}) }, o),
  scrollTo: (o) => withSeq({ hm: 2, type: "scrollTo", y: o.y }, o),
};
