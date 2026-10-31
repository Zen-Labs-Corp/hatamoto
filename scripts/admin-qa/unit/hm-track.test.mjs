// The core beacon (backend/internal/render/hm_track.js) in node:vm with stub browser globals.
// Run: node --test scripts/admin-qa/unit/hm-track.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import vm from "node:vm";
import { readFileSync } from "node:fs";

const src = readFileSync(new URL("../../../backend/internal/render/hm_track.js", import.meta.url), "utf8");

// run executes the beacon once in a fresh "page"; store is the tab's sessionStorage
// (null = storage throws). Returns the posted bodies and the scroll listener.
function run({ engage = true, store = {}, referrer = "", search = "", path = "/landing", host = "example.com", webdriver, preview, forms = [], io = true } = {}) {
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
  const docL = {};
  const observed = [];
  let ioCb = null;
  const ctx = {
    document: {
      referrer,
      documentElement: el,
      addEventListener: (n, f) => (docL[n] = f),
      removeEventListener: (n) => delete docL[n],
      querySelectorAll: () => forms,
      createElement: () => ({}),
    },
    location: { hostname: host, host, search, pathname: path, href: `https://${host}${path}` },
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
  if (io) {
    ctx.IntersectionObserver = class {
      constructor(cb) { ioCb = cb; }
      observe(f) { observed.push(f); }
      disconnect() { observed.length = 0; }
    };
  }
  ctx.window = Object.assign(win, ctx);
  vm.runInNewContext(src.replace("__HM_ENGAGE__", String(engage)), ctx);
  return {
    get events() { return posts.map((p) => JSON.parse(p.blob.text).events[0]); },
    posts,
    scroll(y) { win.pageYOffset = y; listeners.scroll?.(); },
    listeners,
    store,
    docL,
    observed,
    visible() { ioCb?.([{ isIntersecting: true }]); },
  };
}

// A stub <form> with the given action; appended inputs land in .inputs.
function form(action) {
  const f = { inputs: [], getAttribute: () => action, closest: () => f, querySelector: (q) => f.inputs.find((i) => i.name === "hm_attr") || null, appendChild: (i) => f.inputs.push(i) };
  return f;
}
const submit = (r, f) => r.docL.submit({ target: f });
const attr = (f) => JSON.parse(f.inputs.find((i) => i.name === "hm_attr").value);

test("fresh tab with an external referrer is an entry; host only, no URL", () => {
  const r = run({ referrer: "https://www.Google.com/search?q=secret", search: "?utm_source=News&utm_medium=email&utm_campaign=x&gclid=1" });
  assert.equal(r.posts[0].url, "/api/v1/track");
  assert.equal(r.posts[0].blob.type, "text/plain");
  assert.deepEqual(r.events[0], { type: "page_view", ref: "google.com", utm_source: "news", utm_medium: "email", utm_campaign: "x", paid: true, path: "/landing", entry: true });
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

test("first touch survives a second external arrival; last touch follows it", () => {
  const store = {};
  run({ store, referrer: "https://www.perplexity.ai/search?q=x" });
  const r = run({ store, referrer: "https://news.example.org/", search: "?utm_source=Newsletter&utm_medium=email", path: "/second" });
  const f = form("/forms/contact/submit");
  submit(r, f);
  assert.deepEqual(attr(f), {
    f: { h: "perplexity.ai", s: "", m: "", c: "", p: "/landing", g: false },
    l: { h: "news.example.org", s: "newsletter", m: "email", c: "", p: "/second", g: false },
  });
  assert.deepEqual(r.events[0].utm_source, "newsletter");
});

test("a marker from before first touch existed is promoted to first touch", () => {
  const store = { hm_v: JSON.stringify({ r: "https://google.com/", s: "", m: "", c: "", p: "/old", g: false, x: {} }) };
  const r = run({ store, referrer: "https://example.com/old" });
  const f = form("/forms/contact/submit");
  submit(r, f);
  assert.equal(attr(f).f.h, "google.com");
  assert.equal(attr(f).f.p, "/old");
});

test("hm_attr is injected only for same-host form submit actions, once, and never in beacons", () => {
  const r = run({ referrer: "https://google.com/" });
  const mine = form("https://example.com/x/api/forms/contact/submit");
  submit(r, mine);
  submit(r, mine);
  assert.equal(mine.inputs.length, 1, "field replaced, not duplicated");
  for (const action of ["https://other.com/forms/contact/submit", "/search", "/forms/contact/submit/extra", "http://["]) {
    const f = form(action);
    submit(r, f);
    assert.equal(f.inputs.length, 0, action);
  }
  assert.ok(!r.posts.some((p) => p.blob.text.includes("hm_attr")));
  assert.deepEqual(Object.keys(r.events[0]).sort(), ["entry", "path", "paid", "ref", "type", "utm_campaign", "utm_medium", "utm_source"].sort());
});

test("no hm_attr without sessionStorage", () => {
  const r = run({ store: null });
  assert.equal(r.docL.submit, undefined);
});

test("hm_attr is not gated by the engagement setting", () => {
  const r = run({ engage: false, referrer: "https://google.com/" });
  const f = form("/forms/contact/submit");
  submit(r, f);
  assert.equal(attr(f).l.h, "google.com");
});

test("form_start and form_seen are sent once per visit", () => {
  const store = {};
  const f = form("/forms/contact/submit");
  const r = run({ store, referrer: "https://google.com/", forms: [f] });
  assert.deepEqual(r.observed, [f]);
  r.docL.focusin({ target: form("/search") });
  assert.equal(r.events.length, 1, "unrelated form ignored");
  r.docL.focusin({ target: f });
  r.visible();
  assert.deepEqual(r.events.slice(1).map((e) => [e.type, e.kind]), [["engage", "form_start"], ["engage", "form_seen"]]);
  assert.equal(r.docL.focusin, undefined, "listener removed");
  // next page of the same visit: neither step repeats (multi still fires once)
  const r2 = run({ store, referrer: "https://example.com/", forms: [f] });
  assert.deepEqual(r2.events.map((e) => e.kind), [undefined, "multi"]);
  assert.equal(r2.docL.focusin, undefined);
  assert.equal(r2.observed.length, 0);
});

test("no form events with engage off, and without IntersectionObserver only form_start", () => {
  const f = form("/forms/contact/submit");
  const off = run({ engage: false, forms: [f] });
  assert.equal(off.docL.focusin, undefined);
  assert.equal(off.observed.length, 0);
  assert.equal(off.events.length, 1);
  const noIO = run({ forms: [f], io: false });
  noIO.docL.focusin({ target: f });
  assert.deepEqual(noIO.events.map((e) => e.kind), [undefined, "form_start"]);
});

test("webdriver and preview register no form listeners", () => {
  assert.equal(run({ webdriver: true }).docL.submit, undefined);
  assert.equal(run({ preview: true }).docL.focusin, undefined);
});

test("a form event does not take the visit's first flag from a later scroll", () => {
  const f = form("/forms/contact/submit");
  const r = run({ referrer: "https://google.com/", forms: [f] });
  r.docL.focusin({ target: f });
  r.visible();
  r.scroll(700);
  const by = Object.fromEntries(r.events.slice(1).map((e) => [e.kind, e.first]));
  assert.equal(by.form_start, undefined);
  assert.equal(by.form_seen, undefined);
  assert.equal(by.scroll, true);
});
