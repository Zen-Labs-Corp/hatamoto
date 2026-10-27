// Client for plugin admin pages: <script src="/plugin-ui/_sdk/bridge.js"></script>
// Defines window.hatamoto:
//   hatamoto.ready                     resolves with {plugin, functions}
//   hatamoto.route(method, path, body, query)  call one of the plugin's own routes
//                                      -> {status, content_type, body} (body is text)
//   hatamoto.host(fn, args)            call a host function the owner granted
//                                      -> {ok, result|error}   (owner only; an editor's page uses routes)
// The page has no network access of its own; this is its only way out.
(() => {
  const pending = new Map();
  let seq = 0;
  let hello;
  let nonce = ""; // the host's greeting carries it; every request echoes it
  const ready = new Promise((resolve) => {
    hello = resolve;
  });

  addEventListener("message", (e) => {
    if (e.source !== parent) return;
    const m = e.data;
    if (!m || m.hm !== 1) return;
    if (m.type === "hello") {
      nonce = m.nonce || "";
      hello({ plugin: m.plugin, functions: m.functions });
    }
    if (m.type === "result" && pending.has(m.id)) {
      const p = pending.get(m.id);
      pending.delete(m.id);
      if (m.error) p.reject(new Error(m.error));
      else p.resolve(m.result);
    }
  });

  function send(msg) {
    return new Promise((resolve, reject) => {
      const id = ++seq;
      pending.set(id, { resolve, reject });
      parent.postMessage({ hm: 1, id, nonce, ...msg }, "*");
    });
  }

  window.hatamoto = {
    ready,
    route: (method, path, body, query) => send({ op: "route", method, path, body, query }),
    host: (fn, args) => send({ op: "host", fn, args }),
  };
  parent.postMessage({ hm: 1, op: "ready" }, "*");
})();
