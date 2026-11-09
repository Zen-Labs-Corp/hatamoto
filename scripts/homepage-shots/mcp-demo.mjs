// Runs a real MCP session against the seeded instance (an agent drafts a post and publishes
// it), then writes the transcript as a terminal-style HTML page for the README "mcp" shot.
// Every value shown (tool count, ids, status, address, HTTP status) comes from the server.
//   node mcp-demo.mjs KEY_FILE OUT_HTML
// Env: BASE.
import fs from "node:fs";
import { fontFile } from "./frame.mjs";

const [keyFile, outHtml] = process.argv.slice(2);
if (!keyFile || !outHtml) throw new Error("usage: node mcp-demo.mjs KEY_FILE OUT_HTML");
const base = process.env.BASE || "http://127.0.0.1:8090";
const key = fs.readFileSync(keyFile, "utf8").trim();
let session = "", seq = 0;

async function rpc(method, params, notify = false) {
  const body = { jsonrpc: "2.0", method, ...(params ? { params } : {}), ...(notify ? {} : { id: ++seq }) };
  const res = await fetch(`${base}/mcp`, {
    method: "POST",
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json", Accept: "application/json, text/event-stream", ...(session ? { "Mcp-Session-Id": session } : {}) },
    body: JSON.stringify(body),
  });
  session ||= res.headers.get("mcp-session-id") || "";
  const text = await res.text();
  if (notify) return null;
  const line = text.split("\n").find((l) => l.startsWith("data: "));
  const msg = JSON.parse(line ? line.slice(6) : text);
  if (msg.error) throw new Error(`${method}: ${msg.error.message}`);
  return msg.result;
}
// call runs one tool and returns its JSON payload (the first text content block).
async function call(name, args) {
  const r = await rpc("tools/call", { name, arguments: args });
  if (r.isError) throw new Error(`${name}: ${r.content?.[0]?.text}`);
  const t = r.content?.find((c) => c.type === "text")?.text ?? "{}";
  try { return JSON.parse(t); } catch { return { text: t }; }
}

const init = await rpc("initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "publishing-agent", version: "1.0" } });
await rpc("notifications/initialized", null, true);
const tools = (await rpc("tools/list")).tools;
const title = "November product notes";
const created = await call("entities_create", {
  schema: "post",
  name: title,
  attributes: {
    excerpt: "What changed this month, in five minutes.",
    tags: ["releases"],
    content: "<p>Three things shipped this month: faster image resizing, a calmer editor toolbar and scheduled publishing for pages.</p><h2>Faster images</h2><p>Uploads are resized once and served as WebP or AVIF to browsers that take them.</p>",
  },
});
const ent = created.entity ?? created;
const published = await call("entities_publish", { id: ent.id });
const pub = published.entity ?? published;
const addr = pub.url || pub.path || `/blog/${pub.slug || ent.slug}`;
const page = await fetch(base + new URL(addr, base).pathname);

const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const short = (id) => `${String(id).slice(0, 8)}…`;
const rows = [
  ["→", "initialize", `client publishing-agent 1.0`],
  ["←", `${init.serverInfo.name} MCP server`, `protocol ${init.protocolVersion}`],
  ["→", "tools/list", ""],
  ["←", `${tools.length} tools`, ["entities_create", "builder_apply_ops", "media_upload", "seo_analyze"].filter((n) => tools.some((t) => t.name === n)).join(", ") + ", …"],
  ["→", "tools/call entities_create", `{ schema: "post", name: "${title}" }`],
  ["←", `${ent.status ?? "draft"}`, `id ${short(ent.id)}  slug ${ent.slug}`],
  ["→", "tools/call entities_publish", `{ id: "${short(ent.id)}" }`],
  ["←", `${pub.status ?? "published"}`, new URL(addr, base).pathname],
  ["→", `GET ${new URL(addr, base).pathname}`, ""],
  ["←", `${page.status} ${page.statusText}`, `${(await page.text()).length.toLocaleString("en")} bytes of HTML`],
];
const html = `<!doctype html><meta charset="utf-8"><style>
@font-face{font-family:"Geist";src:url(data:font/woff2;base64,${fontFile("Geist-Variable.woff2")});font-weight:100 900}
@font-face{font-family:"Geist Mono";src:url(data:font/woff2;base64,${fontFile("GeistMono-Variable.woff2")});font-weight:100 900}
html,body{margin:0;background:#0f1012;color:#e8e8e3;font-family:Geist,sans-serif}
main{padding:64px 72px}
h1{font-size:22px;font-weight:500;color:#9a9a93;margin:0 0 6px;letter-spacing:.02em}
h1 b{color:#e8e8e3;font-weight:600}
p.k{font:16px "Geist Mono",monospace;color:#6f6f69;margin:0 0 40px}
table{border-collapse:collapse;font:19px/1.5 "Geist Mono",monospace;width:100%}
td{padding:12px 0;vertical-align:top}
td.a{width:44px;color:#6f6f69}
tr.in td.a{color:#e0533d}
tr.out td.a{color:#4cc38a}
td.m{width:520px;color:#f4f4f2}
tr.out td.m{color:#4cc38a}
td.d{color:#a5a59e}
tr.out + tr.in td{border-top:1px solid #1f2023}
</style><main data-mcp-demo>
<h1><b>MCP session</b> · an agent drafts a post and publishes it</h1>
<p class="k">POST /mcp · Authorization: Bearer nxk_•••• (key "Publishing agent": read:entities, write:entities)</p>
<table>${rows.map(([a, m, d]) => `<tr class="${a === "→" ? "in" : "out"}"><td class="a">${a}</td><td class="m">${esc(m)}</td><td class="d">${esc(d)}</td></tr>`).join("")}</table>
</main>`;
fs.writeFileSync(outHtml, html);
console.log(`mcp-demo: ${tools.length} tools, published ${ent.slug} (${page.status})`);
