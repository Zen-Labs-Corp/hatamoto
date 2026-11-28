// Public pages under the default policy: a fresh harness install (no CSP_ENFORCE)
// sends an enforcing Content-Security-Policy and the default theme logs no CSP
// violation on its pages. Run: scripts/admin-qa/run-local.sh csp-public.mjs
import { launch, newSession, base } from "./lib.mjs";

const b = await launch();
let bad = 0;
const ok = (c, m) => { console.log(`${c ? "ok  " : "FAIL"} ${m}`); if (!c) bad++; };

const p = await newSession(b, { signedIn: false }); // bypassCSP defaults to false
const violations = [];
p.on("console", (m) => { if (/content security policy|refused to/i.test(m.text())) violations.push(`${p.url()}: ${m.text()}`); });

const visit = async (path) => {
  const res = await p.goto(`${base}${path}`, { waitUntil: "networkidle" });
  const h = res.headers();
  ok(!!h["content-security-policy"] && !h["content-security-policy-report-only"], `${path}: enforcing CSP header`);
};

await visit("/");
// A second page, found from the home page links (same origin, not the admin).
const next = await p.evaluate((origin) => {
  for (const a of document.querySelectorAll("a[href]")) {
    const u = new URL(a.href, location.href);
    if (u.origin === origin && u.pathname !== "/" && !u.pathname.startsWith("/admin") && !u.hash) return u.pathname;
  }
  return "";
}, new URL(base).origin);
if (next) await visit(next);
else console.log("note: no second same-origin page linked from home");

ok(violations.length === 0, `no CSP violation in the console${violations.length ? ` (${violations.join("; ")})` : ""}`);
await b.close();
process.exit(bad ? 1 : 0);
