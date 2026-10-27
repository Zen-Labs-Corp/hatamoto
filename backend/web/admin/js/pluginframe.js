// Host side of the plugin page bridge. A plugin's admin page runs in a
// sandboxed iframe (opaque origin, no cookies, no network). It asks this script
// for things with postMessage; this script forwards only what the plugin may do
// to the bridge endpoint, which runs as the signed-in user and enforces the
// plugin's granted permissions again on the server.
//
//   iframe -> {hm:1, id, op:"ready"}                         -> {hm:1, type:"hello", plugin, functions, nonce}
//   iframe -> {hm:1, id, nonce, op:"route", method, path, query, body}  (the plugin's own routes)
//   iframe -> {hm:1, id, nonce, op:"host", fn, args}                    (a granted host function; owner only)
//
// The bridge belongs to the ONE document the frame loads first. A sandboxed
// frame can still navigate itself, and the next document has the same opaque
// origin, so it could keep using the bridge. The frame is therefore created
// here, with its load listener in place before it starts loading; the first
// load is the plugin's page, and any later load means it navigated: relaying
// stops for good, the frame is blanked (its one-time URL ticket is spent, so
// it cannot be loaded again) and a notice asks for a reload. Replies go only to the frame's own window, only while it
// is still in that first document, and every request must carry the nonce the
// first document was greeted with.
//
// Not stopped here: a page can still leave through ONE navigation, with
// whatever it had already read in the URL (docs/PLUGIN_API.md, "Admin page").
function init() {
  const holder = document.querySelector("[data-hm-pluginframe]");
  if (!holder || holder.dataset.bound) return;
  holder.dataset.bound = "1";
  const tpl = holder.querySelector("template");
  const frame = tpl?.content.querySelector("iframe")?.cloneNode(true);
  if (!frame) return;
  const src = holder.dataset.src;
  const plugin = holder.dataset.plugin;
  const bridge = holder.dataset.bridge;
  const functions = new Set((holder.dataset.functions || "").split(",").filter(Boolean));
  const notice = holder.querySelector("[data-hm-pluginframe-notice]");
  const csrf = () => document.querySelector('meta[name="csrf-token"]')?.content ?? "";

  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const nonce = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  let loads = 0;
  let dead = false;

  const reply = (msg) => {
    if (!dead && loads <= 1) frame.contentWindow?.postMessage({ hm: 1, ...msg }, "*");
  };

  async function forward(m) {
    let body;
    if (m.op === "route") {
      if (typeof m.path !== "string" || !m.path.startsWith("/")) throw new Error("path must start with /");
      body = { op: "route", method: String(m.method || "GET").toUpperCase(), path: m.path, query: m.query || {}, body: typeof m.body === "string" ? m.body : m.body == null ? "" : JSON.stringify(m.body) };
    } else if (m.op === "host") {
      if (!functions.has(m.fn)) return { ok: false, error: { code: "permission_denied", message: `${m.fn} is not available to this plugin's page` } };
      body = { op: "host", fn: m.fn, args: m.args ?? {} };
    } else {
      throw new Error("unknown op");
    }
    const res = await fetch(bridge, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() },
      body: JSON.stringify(body),
    });
    return res.json();
  }

  // The frame may announce itself before its load event, so the host greets it
  // on "ready" and again when the first load has finished.
  const hello = () => reply({ type: "hello", plugin, functions: [...functions], nonce });

  frame.addEventListener("load", () => {
    loads++;
    if (loads === 1) {
      hello();
      return;
    }
    if (!dead) {
      dead = true;
      frame.src = "about:blank"; // the ticket in src is spent; the page is no longer bridged
      if (notice) notice.hidden = false;
    }
  });

  window.addEventListener("message", async (e) => {
    if (dead || loads > 1 || !frame.contentWindow || e.source !== frame.contentWindow) return;
    const m = e.data;
    if (!m || m.hm !== 1 || typeof m.op !== "string") return;
    if (m.op === "ready") {
      hello();
      return;
    }
    if (m.nonce !== nonce) return;
    try {
      const result = await forward(m);
      reply({ type: "result", id: m.id, result });
    } catch (err) {
      reply({ type: "result", id: m.id, error: String(err?.message || err) });
    }
  });

  frame.src = src;
  tpl.replaceWith(frame);
}

init();
document.addEventListener("htmx:afterSettle", init);
