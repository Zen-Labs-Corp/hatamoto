// The core beacon (backend/internal/render/hm_track.js) in node:vm with stub browser globals.
// Run: node --test scripts/admin-qa/unit/hm-track.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/internal/render/hm_track.js", import.meta.url), "utf8");

// run executes the beacon once in a fresh "page"; store is the tab's sessionStorage
// (null = storage throws). Returns the posted bodies and the scroll listener.
function run({ engage = true, store = {}, referrer = "", search = "", path = "/landing", host = "example.com", webdriver, preview } = {}) {
  const posts = [];
  const listeners = {};
  const win = {
    pageYOffset: 0,
    innerHeight: 100,
    addEventListener: (n, f) => (listeners[n] = f),
    removeEventListener: (n) => delete listeners[n],
    fetch: undefined,
  };
  const el = { scrollHeight: 1000, scrollTop: 0, clientHeight: 100, dataset: preview ? { hmPreview: "true" } : {} };
  const ctx = {
    document: { referrer, documentElement: el },
    location: { hostname: host, search, pathname: path },
    navigator: {
      webdriver,
      sendBeacon: (url, blob) => (posts.push({ url, blob }), true),
    },
    sessionStorage: store
      ? { getItem: (k) => (k in store ? store[k] : null), setItem: (k, v) => (store[k] = v) }
      : { getItem() { throw new Error("blocked"); }, setItem() { throw new Error("blocked"); } },
    self: { origin: "https://example.com" },
    Blob: class { constructor(parts, o) { this.text = parts.join(""); this.type = o.type; } },
    URL, URLSearchParams, JSON, String, Object,
  };
  ctx.window = Object.assign(win, ctx);
  vm.runInNewContext(src.replace("__HM_ENGAGE__", String(engage)), ctx);
  return {
    get events() { return posts.map((p) => JSON.parse(p.blob.text).events[0]); },
    posts,
    scroll(y) { win.pageYOffset = y; listeners.scroll?.(); },
    listeners,
    store,
  };
}

test("fresh tab with an external referrer is an entry; host only, no URL", () => {
  const r = run({ referrer: "https://www.Google.com/search?q=secret", search: "?utm_source=News&utm_medium=email&utm_campaign=x&gclid=1" });
  assert.equal(r.posts[0].url, "/api/v1/track");
  assert.equal(r.posts[0].blob.type, "text/plain");
  assert.deepEqual(r.events[0], { type: "page_view", ref: "google.com", us: "news", um: "email", uc: "x", paid: true, path: "/landing", entry: true });
  assert.ok(!r.posts[0].blob.text.includes("secret"));
});

test("fresh tab without referrer is an entry (direct)", () => {
  const r = run();
  assert.equal(r.events[0].entry, true);
  assert.equal(r.events[0].ref, "");
});

test("same-tab internal navigation is not an entry and sends engage/multi once", () => {
  const store = {};
  run({ store, referrer: "https://google.com/" });
  const r = run({ store, referrer: "https://example.com/landing", path: "/two" });
  assert.equal(r.events[0].entry, false);
  assert.equal(r.events[0].ref, "google.com", "pageviews carry the visit's attributes");
  assert.equal(r.events[0].path, "/landing");
  assert.deepEqual([r.events[1].type, r.events[1].kind, r.events[1].first], ["engage", "multi", true]);
  const r3 = run({ store, referrer: "https://example.com/two" });
  assert.equal(r3.events.length, 1, "multi is once per visit");
});

test("new external referrer in the same tab starts a new visit; a reload with the same one does not", () => {
  const store = {};
  run({ store, referrer: "https://google.com/" });
  assert.equal(run({ store, referrer: "https://bing.com/" }).events[0].entry, true);
  assert.equal(run({ store, referrer: "https://bing.com/" }).events[0].entry, false);
});

test("webdriver and preview send nothing", () => {
  assert.equal(run({ webdriver: true }).posts.length, 0);
  assert.equal(run({ preview: true }).posts.length, 0);
});

test("blocked sessionStorage falls back to ext || no referrer, and sends no multi", () => {
  assert.equal(run({ store: null }).events[0].entry, true);
  assert.equal(run({ store: null, referrer: "https://google.com/" }).events[0].entry, true);
  const r = run({ store: null, referrer: "https://example.com/a" });
  assert.equal(r.events[0].entry, false);
  assert.equal(r.events.length, 1);
});

test("scroll past 75% sends engage/scroll once, first only on the first engagement", () => {
  const r = run({ referrer: "https://google.com/" });
  r.scroll(300);
  assert.equal(r.events.length, 1);
  r.scroll(700);
  assert.deepEqual([r.events[1].kind, r.events[1].first], ["scroll", true]);
  assert.equal(r.listeners.scroll, undefined, "listener removed");
  // a later page of the same visit: multi is not the first engagement any more
  const r2 = run({ store: r.store, referrer: "https://example.com/" });
  assert.equal(r2.events[1].kind, "multi");
  assert.equal(r2.events[1].first, undefined);
});

test("ENGAGE=false sends only the pageview and adds no scroll listener", () => {
  const store = {};
  run({ store, engage: false });
  const r = run({ store, engage: false, referrer: "https://example.com/" });
  assert.equal(r.events.length, 1);
  assert.equal(r.listeners.scroll, undefined);
});

test("sandboxed pages post with a credential-less fetch", async () => {
  const bodies = [];
  const ctx = {
    document: { referrer: "", documentElement: { dataset: {} } },
    location: { hostname: "a.com", search: "", pathname: "/" },
    navigator: { sendBeacon() { throw new Error("no beacon"); } },
    sessionStorage: { getItem: () => null, setItem() {} },
    self: { origin: "null" },
    fetch: (u, o) => (bodies.push([u, o]), Promise.resolve()),
    URL, URLSearchParams, JSON, String, Object,
  };
  ctx.window = { ...ctx, addEventListener() {}, removeEventListener() {} };
  vm.runInNewContext(src.replace("__HM_ENGAGE__", "true"), ctx);
  assert.equal(bodies[0][1].credentials, "omit");
  assert.equal(bodies[0][1].keepalive, true);
});
