// Inline rich text of a heading/text node: the canvas posts either plain text (prop "text")
// or marked-up html (prop "html"). The parent never parses the html; it stores it as given
// (Go sanitises on save and at render) and keeps `text` as the plain projection for search,
// MCP and hints. Pure.
import { find } from "@hm/builder/store";

const ENT = { lt: "<", gt: ">", nbsp: "\u00a0", amp: "&" };

/** The plain text of bridge-serialised html (tags dropped, <br> as a newline, its entities decoded). */
export const textOf = (html) => html.replace(/<br>/g, "\n").replace(/<[^>]*>/g, "").replace(/&(\w+);/g, (e, n) => ENT[n] ?? e);

const NAMED = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: "\u00a0" };
const unent = (s) =>
  s.replace(/&(?:#x([0-9a-f]+)|#(\d+)|(\w+));/gi, (e, x, d, n) => {
    if (n) return NAMED[n.toLowerCase()] ?? e;
    const c = x ? parseInt(x, 16) : +d;
    return c > 0 && c <= 0x10ffff ? String.fromCodePoint(c) : e;
  });
const escT = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
/** The href as Go writes it: lower-case scheme; space, non-ASCII and "<>`\\^{|} percent-encoded (the bridge's inlineHref). */
const hrefOf = (h) => {
  h = h
    .trim()
    .replace(/^[A-Za-z][A-Za-z0-9+.-]*:/, (m) => m.toLowerCase())
    // only the last @ ends the userinfo
    .replace(/^(https?:\/\/)([^/?#]*)/i, (m, p, a) => {
      const k = a.lastIndexOf("@");
      return k < 0 ? m : p + a.slice(0, k).replace(/@/g, "%40") + a.slice(k);
    });
  const f = h.indexOf("#");
  // a second # in the fragment is escaped
  if (f >= 0) h = h.slice(0, f + 1) + h.slice(f + 1).replace(/#/g, "%23");
  return h.replace(/[^\x21-\x7e]+|["<>`\\^{|}]+/g, (m) => {
    try {
      return encodeURIComponent(m);
    } catch {
      return m;
    }
  });
};
const MARKS = new Set(["strong", "em", "b", "i", "u", "s", "code"]);

/**
 * The canonical inline html (the form Go's SanitizeInlineHTML and the bridge's serialiser write; the shared
 * fixture backend/testdata/inline keeps them in step) of an editor's output: text escapes & < > only (`'`, `"` and
 * a no-break space literal), <br> without a slash, links as href then target/rel, no other attributes.
 */
export function canonHTML(html) {
  let out = "";
  let at = 0;
  const tag = /<(\/?)([a-zA-Z][a-zA-Z0-9]*)((?:"[^"]*"|'[^']*'|[^>"'])*)>/g;
  for (let m; (m = tag.exec(html)); at = tag.lastIndex) {
    out += escT(unent(html.slice(at, m.index)));
    const name = m[2].toLowerCase();
    if (name === "br") out += m[1] ? "" : "<br>";
    else if (MARKS.has(name)) out += `<${m[1]}${name}>`;
    else if (name === "a" && m[1]) out += "</a>";
    else if (name === "a") {
      const at2 = {};
      for (const a of m[3].matchAll(/([a-zA-Z-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g)) at2[a[1].toLowerCase()] = unent(a[2] ?? a[3]);
      out += `<a href="${escT(hrefOf(at2.href ?? "")).replace(/"/g, "&quot;")}"` + (at2.target === "_blank" ? ' target="_blank" rel="noopener noreferrer"' : "") + ">";
    }
  }
  return out + escT(unent(html.slice(at)));
}

/**
 * The ops for one canvas text message. Both keys are always written (html is removed by a null)
 * so the history entry of an editing session, which keeps its first inverse, restores both.
 */
export const inlineOps = (node, prop, v) => [
  { op: "setProp", id: node.id, key: "text", value: prop === "html" ? textOf(v) : v },
  { op: "setProp", id: node.id, key: "html", value: prop === "html" ? v : null },
];

/**
 * Ops that change nothing but make the server render node `n` again (the commit's ops decide what is rendered):
 * its text/html, else its type (components) or first prop, each set to the value it has. Empty when it has no props.
 */
export function repatchOps(n) {
  const p = n?.p ?? {};
  const keys = ["text", "html"].filter((k) => k in p);
  // a component always has `type` (planCommit skips props it does not render, e.g. navlabel)
  return (keys.length ? keys : "type" in p ? ["type"] : Object.keys(p).slice(0, 1)).map((key) => ({ op: "setProp", id: n.id, key, value: p[key] }));
}

/**
 * The edit session of canvas text: run(ops, commitOpts, syncOpts) commits, repatch(id) shows the server's markup of a node (renderer: the sync renderer; doc(): the current doc).
 * Typing sends no patch (the caret must not move); end(id) does, once, when the edit is over. Every message of a session
 * shares one undo key, whichever of text/html it carries, and the next session starts a new one.
 */
export function mountInline({ run, doc, renderer }) {
  const typed = new Set();
  let seq = 0;
  // Show the server's markup of a node again: a commit of its own values (nothing changes), else the whole doc.
  const repatch = (id) => {
    const ops = repatchOps(find(doc(), id));
    return ops.length ? renderer.commit(doc(), doc(), ops) : renderer.stage();
  };
  return {
    repatch,
    /** The frame was swapped: its edit is gone, so the next one starts a new undo step. */
    reset() {
      seq++;
      typed.clear();
    },
    text(id, prop, value, plain) {
      if (plain) typed.add(id);
      run(inlineOps({ id }, prop, value), { coalesce: `inline:${id}:${seq}`, label: "Change Text" }, { inline: true });
    },
    end(id) {
      seq++;
      if (typed.delete(id)) repatch(id);
    },
  };
}
