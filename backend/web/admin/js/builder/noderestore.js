// "Restore from an earlier version…" (lazy): a modal that lists the saved versions holding this element and puts
// the chosen one back as one commit. The list is a GET (the node-versions route); nothing is written to the server.
import { h } from "@hm/builder/dom";
import { hmDate, hmTime } from "@hm/dates";
import { clone } from "@hm/builder/savedialog";
import { locate } from "@hm/builder/store";
import { restoreOps } from "@hm/builder/restoreops";

/** "Today 16:02", "Yesterday 16:02", else "12 Oct 2026 16:02" (the admin's house format, @hm/dates). */
export function when(iso, now = new Date()) {
  const d = new Date(iso);
  const days = Math.round((new Date(now.getFullYear(), now.getMonth(), now.getDate()) - new Date(d.getFullYear(), d.getMonth(), d.getDate())) / 864e5);
  const day = days === 0 ? "Today" : days === 1 ? "Yesterday" : hmDate(d);
  return `${day} ${hmTime(d)}`;
}

/** A short sample of what the element said then: its text without markup, else the kind. */
export function sample(node, kindName) {
  const raw = ["text", "title", "label", "html", "navlabel"].map((k) => node.p?.[k]).find((v) => typeof v === "string" && v.trim());
  const text = raw?.replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim();
  return text ? (text.length > 60 ? `${text.slice(0, 59)}…` : text) : kindName(node.t);
}

/** The row to choose first: the newest version that differs from the element now (the latest save is often the same). */
export const defaultVersion = (rows, now) => Math.max(0, rows.findIndex((r) => now && restoreOps(now.node, r.node, now).length));

/** The dialog title: the element's outline name when it has one. */
export const restoreTitle = (label, kind) => `Restore “${label || kind}” from an earlier version`;

/**
 * openRestore({id, doc(), url, kindName, label(id), commit(ops, {label}) -> bool, announce, returnFocus(id)}). The dialog
 * closes when the element is back (focus goes to returnFocus: the opener is gone by then); "Nothing to restore" and a
 * restore the store refuses keep it open with a message. The newest version that differs from the page now is chosen.
 */
export function openRestore({ id, doc, url, kindName, label = () => "", commit, announce, returnFocus = () => {} }) {
  const start = locate(doc(), id);
  if (!start) return;
  const name = kindName(start.node.t);
  const cancel = clone("act", "Cancel", { type: "button" });
  const ok = clone("actp", "Restore this element", { type: "submit", disabled: "" });
  const status = h("div", { role: "status", class: "hm-bs__skeleton" }, h("span"), h("span"), h("span"), h("span", { class: "hm-visually-hidden", text: "Loading earlier versions…" }));
  const body = h("div", { class: "hm-dialog__body" }, status);
  const problem = h("p", { class: "hm-field__error", role: "alert", hidden: true });
  const form = h("form", { method: "dialog", class: "hm-dialog__panel" }, h("h2", { class: "hm-dialog__title", id: "hm-bs-rs-t", text: restoreTitle(label(id), name) }), body, problem, h("div", { class: "hm-dialog__foot" }, cancel, ok));
  const dlg = h("dialog", { class: "hm-dialog", "aria-labelledby": "hm-bs-rs-t", "data-size": "sm" }, form);
  let rows = [];
  const say = (msg) => {
    status.replaceChildren(h("p", { class: "hm-muted", text: msg }));
    status.classList.remove("hm-bs__skeleton");
  };
  cancel.addEventListener("click", () => dlg.close());
  dlg.addEventListener("close", () => dlg.remove());
  form.addEventListener("change", () => (problem.hidden = true));
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const row = rows[Number(form.elements.version?.value)];
    const cur = locate(doc(), id);
    if (!row || !cur) return;
    const ops = restoreOps(cur.node, row.node, cur);
    if (!ops.length) {
      problem.textContent = "Nothing to restore: the element is the same as in that version.";
      problem.hidden = false;
      return;
    }
    if (!commit(ops, { label: `Restore ${name} from version ${row.version}` })) {
      problem.textContent = "This element cannot be restored here.";
      problem.hidden = false;
      return;
    }
    announce(`${name} restored from ${when(row.saved)}`);
    dlg.close();
    returnFocus(id);
  });
  document.body.append(dlg);
  dlg.showModal();
  cancel.focus();
  fetch(url, { credentials: "same-origin", headers: { accept: "application/json" } })
    .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.status))))
    .then((list) => {
      rows = list;
      if (!rows.length) return say("No earlier version of this element is saved.");
      const first = defaultVersion(rows, locate(doc(), id));
      const items = rows.map((r, i) =>
        h("label", { class: "hm-radiocard" }, h("input", { class: "hm-radiocard__input", type: "radio", name: "version", value: String(i), checked: i === first }),
          h("span", { class: "hm-radiocard__body" }, h("span", { class: "hm-radiocard__title", text: `${when(r.saved)}${r.by ? ` · ${r.by}` : ""}` }), h("span", { class: "hm-muted", text: sample(r.node, kindName) }))),
      );
      body.replaceChildren(h("fieldset", { class: "hm-radiocards" }, h("legend", { class: "hm-visually-hidden", text: "Earlier versions" }), ...items));
      ok.removeAttribute("disabled");
      items[first].querySelector("input").focus();
    })
    .catch(() => say("Earlier versions could not be loaded. Try again."));
}
