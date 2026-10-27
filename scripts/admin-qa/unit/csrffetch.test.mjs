import { test } from "node:test";
import assert from "node:assert/strict";
import { installCsrfFetch } from "../../../backend/web/admin/js/shelllogic.js";

// A fake window: fetch records what reaches the network.
function rig({ base = "/x-test", token = "tok" } = {}) {
  const calls = [];
  const g = {
    location: { href: "https://site.example/x-test/entities", origin: "https://site.example" },
    fetch: async (input, init) => { calls.push({ input, init }); return "ok"; },
  };
  installCsrfFetch(g, base, token);
  return { g, calls, sent: (i = 0) => new Headers(calls[i].init?.headers).get("X-CSRF-Token") };
}

test("adds the token to same-origin admin fetches, relative and absolute", async () => {
  const r = rig();
  await r.g.fetch("/x-test/entities/1/save", { method: "POST" });
  await r.g.fetch("https://site.example/x-test/media");
  await r.g.fetch(new URL("https://site.example/x-test"));
  await r.g.fetch("partial?x=1"); // relative to /x-test/entities
  assert.deepEqual([0, 1, 2, 3].map((i) => r.sent(i)), ["tok", "tok", "tok", "tok"]);
  assert.equal(r.calls[0].init.method, "POST");
});

test("leaves other paths, assets, other origins and prefix lookalikes alone", async () => {
  const r = rig();
  for (const u of ["/other", "/x-test/_/admin.js", "https://evil.example/x-test/a", "/x-testing/a", "/api/v1/me"]) await r.g.fetch(u);
  assert.deepEqual(r.calls.map((_, i) => r.sent(i)), [null, null, null, null, null]);
});

test("keeps an existing header, and merges with Request headers and other init headers", async () => {
  const r = rig();
  await r.g.fetch("/x-test/a", { headers: { "X-CSRF-Token": "mine" } });
  await r.g.fetch("/x-test/b", { headers: { Accept: "text/html" } });
  await r.g.fetch(new Request("https://site.example/x-test/c", { headers: { "X-Other": "1" } }));
  assert.equal(r.sent(0), "mine");
  assert.equal(new Headers(r.calls[1].init.headers).get("Accept"), "text/html");
  assert.equal(r.sent(1), "tok");
  assert.equal(new Headers(r.calls[2].init.headers).get("X-Other"), "1");
  assert.equal(r.sent(2), "tok");
});

test("an empty token adds nothing and leaves fetch unwrapped", async () => {
  const r = rig({ token: "" });
  assert.equal(r.g.fetch.c, undefined);
  await r.g.fetch("/x-test/a");
  assert.equal(r.calls[0].init, undefined);
});

test("installs once and tolerates a missing fetch", async () => {
  const r = rig();
  const first = r.g.fetch;
  installCsrfFetch(r.g, "/x-test", "other");
  assert.equal(r.g.fetch, first);
  assert.doesNotThrow(() => installCsrfFetch({ fetch() {} }, "/a", ""));
});

test("init headers as an array are kept", async () => {
  const r = rig();
  await r.g.fetch("/x-test/a", { headers: [["Accept", "text/html"]] });
  assert.equal(new Headers(r.calls[0].init.headers).get("Accept"), "text/html");
  assert.equal(r.sent(0), "tok");
});
