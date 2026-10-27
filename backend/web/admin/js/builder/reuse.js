// Reuse in the builder screen (a lazy chunk: first paint only needs reusecore.js): the palette's Patterns and Blocks groups
// (boot data, no fetch until one is inserted), the actions under the palette
// (save a selection or the page as a template, save a selection as a block) and block/part
// mode. The ops come from reuseops.js; every action is one commit, so one undo step.
// Names are asked in a small dialog (savedialog.js, no prompt()), server ids are only ever put in URLs.
import { adminURL } from "@hm/adminpath";
import { h, actButton } from "@hm/builder/dom";
import { canMakeBlock, detachOps, makeBlockOps, refNode, templateSource } from "@hm/builder/reuseops";
import { UUID, refState, wantsSlot } from "@hm/builder/reusecore";
import { refName } from "@hm/builder/kindnames";
import { ask } from "@hm/builder/savedialog";

const enc = encodeURIComponent;

/** A JSON request to the admin: the parsed body, or an Error with the server's own message. */
async function call(url, init = {}) {
  const res = await fetch(url, { credentials: "same-origin", ...init });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || "That did not work. Try again.");
  return body;
}

/** The usage line: "Used on 1 page", "Used on 3 pages", "Used on 200+ pages", "Not used on any page yet". */
export const usageSummary = (n, capped) => (n === 0 ? "Not used on any page yet" : `Used on ${n}${capped ? "+" : ""} ${n === 1 && !capped ? "page" : "pages"}`);

/**
 * The Content panel of a ref node: block name, Edit block (new tab), Detach (one undo step)
 * and the usage list. reuse is boot.reuse; commit(ops, opts) -> bool; select(id); announce(msg).
 * A block missing from the boot list is looked up once before it is called missing.
 */
export function refPanel(node, { reuse, getDoc, commit, select, announce }) {
  const blocks = reuse?.blocks ?? [];
  const base = reuse?.urls?.blocks ?? "";
  const st = refState(node, blocks);
  let busy = false;
  const head = h("p", { class: "hm-bs-refname" });
  const msg = h("p", { class: "hm-muted", role: "status" });
  const edit = h("a", { target: "_blank", rel: "noopener", text: "Edit block (opens in a new tab)" });
  const detach = actButton("Detach", { onclick: () => doDetach() });
  const use = actButton("Where is it used?", { onclick: () => loadUsage() });
  const list = h("ul", { class: "hm-bs-usage" });
  const url = (tail = "") => `${base}/${enc(st.id)}${tail}`;

  function paint() {
    head.textContent = st.missing ? "Missing block. It was deleted, or it is not a block. Remove this element or add another block." : refName(st.name);
    edit.hidden = st.missing;
    if (!st.missing) edit.href = adminURL(`/entities/${st.id}/edit`);
    detach.disabled = use.disabled = st.missing || busy;
  }

  async function doDetach() {
    if (busy || st.missing) return;
    busy = true;
    paint();
    msg.textContent = "Detaching…";
    try {
      const { nodes } = await call(url());
      if (commit(detachOps(getDoc(), node.id, nodes), { label: "Detach block" })) {
        select(nodes[0]?.id ?? null);
        announce(`Detached ${st.name}`);
      }
      msg.textContent = "";
    } catch (err) {
      msg.textContent = err.message;
    } finally {
      busy = false;
      paint();
    }
  }

  async function loadUsage() {
    if (busy || st.missing) return;
    busy = true;
    paint();
    msg.textContent = "Loading usage…";
    try {
      const { users, capped } = await call(url("/usage"));
      list.replaceChildren(...users.map((u) => h("li", {}, UUID.test(u.id) ? h("a", { href: adminURL(`/entities/${u.id}/edit`), target: "_blank", rel: "noopener", text: u.name || "Untitled" }) : u.name, ` ${u.type}`)));
      msg.textContent = usageSummary(users.length, capped);
    } catch (err) {
      msg.textContent = err.message;
    } finally {
      busy = false;
      paint();
    }
  }

  paint();
  if (st.missing && st.valid) {
    msg.textContent = "Checking the block…";
    call(url())
      .then((b) => {
        st.name = b.name;
        st.missing = false;
        blocks.push({ id: st.id, name: b.name });
        msg.textContent = "";
      })
      .catch(() => {
        msg.textContent = "";
      })
      .finally(paint);
  }
  return h("div", { class: "hm-bs-content hm-bs-ref" }, head, h("p", {}, edit), h("div", { class: "hm-bs-tools" }, detach, use), msg, list);
}

/**
 * mountReuse(root, {reuse, entity, kinds, csrf, getDoc, getSelected, run, select, announce, repaint}) -> {nodes, refresh}
 * run(ops, opts) commits one history entry (false when refused); repaint() redraws the palette groups.
 * nodes(item) resolves the nodes a template or block inserts; refresh() follows the selection.
 */
export function mountReuse(root, { reuse, kinds, csrf, getDoc, getSelected, run, select, announce, repaint }) {
  const urls = reuse.urls;
  const post = (url, body) => call(url, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() }, body: JSON.stringify(body) });

  const status = h("p", { class: "hm-muted hm-bs-reuse__s", role: "status" });
  const say = (msg) => {
    status.textContent = msg;
  };
  const bSel = actButton("Save as template…", { onclick: () => open("tpl-sel") });
  const bPage = actButton("Save page as template…", { onclick: () => open("tpl-page") });
  const bBlk = actButton("Save as block…", { onclick: () => open("blk") });
  const note = wantsSlot(reuse) && h("p", { class: "hm-muted", role: "note", text: `Place the Content slot once: it marks where the ${reuse.slot === "archive" ? "list of posts" : "page content"} goes.` });
  root.append(h("div", { class: "hm-bs-reuse" }, h("div", { class: "hm-bs-reuse__row" }, bSel, bPage, bBlk), status, note));

  const ASK = {
    "tpl-sel": { title: "Save as template", label: "Template name", ok: "Save template" },
    "tpl-page": { title: "Save page as template", label: "Template name", ok: "Save template" },
    blk: { title: "Save as block", label: "Block name", ok: "Save block" },
  };

  /** Asks for a name in a dialog, then saves; a failure shows in the dialog and nothing closes. */
  function open(m) {
    say("");
    return ask({ ...ASK[m], onSubmit: (name) => (m === "blk" ? makeBlock(name) : saveTemplate(name, m === "tpl-page")) });
  }

  async function saveTemplate(name, wholePage) {
    const doc = getDoc();
    const src = wholePage ? { page: true } : templateSource(doc, getSelected());
    const t = await post(urls.saveTemplate, { name, ...src, doc });
    (reuse.templates ??= []).push({ id: t.id, name: t.name, kind: "tree" });
    repaint();
    say(`Saved template ${t.name}.`);
    announce(`Saved template ${t.name}`);
  }

  async function makeBlock(name) {
    const nodeId = getSelected();
    const b = await post(urls.makeBlock, { name, nodeId, doc: getDoc() });
    (reuse.blocks ??= []).push({ id: b.id, name: b.name });
    repaint();
    let ops;
    try {
      ops = makeBlockOps(getDoc(), nodeId, b.id);
    } catch {
      return say(`Created the block ${b.name}, but the element changed meanwhile. Add the block from Blocks.`);
    }
    if (!run(ops, { label: "Save as block" })) return;
    select(ops[1].node.id);
    say(`Saved block ${b.name}.`);
    announce(`Saved block ${b.name}`);
  }

  return {
    /** The nodes a palette item inserts: a block is one ref, a template is fetched with fresh ids. */
    async nodes(item) {
      const id = item.key.slice(4);
      if (item.key.startsWith("blk:")) return [refNode(id)];
      const t = await call(`${urls.template}/${enc(id)}`);
      return t.nodes;
    },
    say,
    /** Asks for a name to save the selection as a template ("tpl-sel") or as a block ("blk"). */
    open,
    refresh() {
      const doc = getDoc();
      const sel = getSelected();
      bSel.disabled = !sel;
      bBlk.disabled = !canMakeBlock(doc, sel, kinds);
    },
  };
}
