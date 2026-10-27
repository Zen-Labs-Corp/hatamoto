// Site styles: the two form bodies are whole (all ten Colours and fonts fields, every stored token), fonts and the theme
// accent pass through untouched, the palette update, change detection and the colour/corner checks.
// Run: node --test scripts/admin-qa/unit/builder-sitestyles.test.mjs
import { register } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";

const root = new URL("../../../backend/web/admin/js/", import.meta.url).href;
register(
  "data:text/javascript," +
    encodeURIComponent(`export async function resolve(s, c, next) {
      if (s.startsWith("@hm/")) return { url: ${JSON.stringify(root)} + s.slice(4) + ".js", shortCircuit: true };
      return next(s, c);
    }`),
);
globalThis.document = { querySelector: () => null, createElement: () => ({}) };
const S = await import(new URL("builder/sitestyles.js", root));

const ss = () => ({
  theme: { primary: "#0ea5e9", secondary: "#64748b", accent: "#f59e0b", background: "#ffffff", text: "#1e293b", font: "Inter, sans-serif", heading_font: "Georgia, serif", radius_sm: "4px", radius_md: "8px", radius_lg: "16px" },
  tokens: { color_surface: "#ffffff", color_muted: "#eef0f4", color_accent: "#c8102e", space_4: "1.25rem", fs_xl: "1.5rem", shadow_sm: "0 1px 2px rgba(0,0,0,.1)" },
});

test("the body carries the token and only the edited fields", () => {
  const b = S.body({ primary: "#112233", radius_md: "10px" }, "tok");
  assert.equal(b.get("_csrf"), "tok");
  assert.deepEqual([...b.keys()].filter((k) => k !== "_csrf").sort(), ["primary", "radius_md"], "fonts and unedited fields are not posted");
  assert.equal(b.get("primary"), "#112233");
  assert.equal(S.body({ color_accent: "#00aa00" }, "t").has("space_4"), false, "stored tokens are not posted back");
});

test("palette colours come from the stored values by palette name", () => {
  assert.deepEqual(S.paletteColours(ss(), {}), { primary: "#0ea5e9", bg: "#ffffff", text: "#1e293b", accent: "#c8102e", surface: "#ffffff", muted: "#eef0f4" });
  assert.equal(S.paletteColours(ss(), { background: "#000000", color_accent: "#00aa00" }).bg, "#000000");
  assert.equal(S.paletteColours(ss(), { color_accent: "#00aa00" }).accent, "#00aa00");
});

test("changes lists only what differs, per form, ignoring hex case", () => {
  const v = { primary: "#0EA5E9", secondary: "#000000", radius_sm: "4px", color_accent: "#c8102e", color_muted: "#ffffff" };
  assert.deepEqual(S.changes(ss(), v), { theme: { secondary: "#000000" }, tokens: { color_muted: "#ffffff" } });
});

test("validate checks colours and corners like the server does", () => {
  assert.deepEqual(S.validate({ primary: "#abc", text: "#AABBCC", color_accent: "#c8102e80", radius_sm: "0.5rem", radius_lg: "12px" }), {});
  const e = S.validate({ primary: "red", background: "#12", color_accent: "", radius_sm: "round", radius_md: "8", radius_lg: "1000px" });
  assert.deepEqual(Object.keys(e).sort(), ["background", "color_accent", "primary", "radius_lg", "radius_md", "radius_sm"]);
});

test("longHex expands three digits and lower-cases", () => {
  assert.equal(S.longHex("#ABC"), "#aabbcc");
  assert.equal(S.longHex("#AABBCC"), "#aabbcc");
});

test("Secondary carries the same note as the Theme page (not every theme uses it)", () => {
  assert.equal(S.THEME_COLOURS.find((c) => c[0] === "secondary")[1], "Secondary colour (for themes that use it)");
});

test("post: 204 is saved, 422 gives the field errors, other failures throw the server's own message", async () => {
  const reply = (status, json) => (globalThis.fetch = async () => ({ status, json: async () => { if (json === undefined) throw new Error("not json"); return json; } }));
  reply(204);
  assert.deepEqual(await S.post("/x", new URLSearchParams()), {});
  reply(422, { errors: { primary: "Choose a colour." } });
  assert.deepEqual(await S.post("/x", new URLSearchParams()), { errors: { primary: "Choose a colour." } });
  reply(403, { code: "forbidden", error: "Only the owner can change the site styles." });
  await assert.rejects(S.post("/x", new URLSearchParams()), { message: "Only the owner can change the site styles." });
  reply(500, { code: "internal", error: "The theme could not be saved." });
  await assert.rejects(S.post("/x", new URLSearchParams()), { message: "The theme could not be saved." });
  reply(422, { errors: {} });
  await assert.rejects(S.post("/x", new URLSearchParams()), /could not be saved/);
  reply(502);
  await assert.rejects(S.post("/x", new URLSearchParams()), /could not be saved/);
});
