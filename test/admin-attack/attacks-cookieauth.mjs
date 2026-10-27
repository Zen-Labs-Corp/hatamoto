// AT-03b: AT-03 again with ENABLE_COOKIE_AUTH=true (API cookie sessions on).
// The owner signs in through the admin form. A script on a public page does a credentialed GET of
// /api/v1/users and a forged double-submit CSRF pair POST. Expected: 401 or 403 and no write.
// The control uses the cookie path: a session made by POST /api/v1/auth/login (the API's own cookie)
// in the same browser context lets the same GET succeed and a POST with the bound token succeed, so the
// probe is proven able to see a cookie-authenticated success. Without it the result is INCONCLUSIVE.
// Run against the server pinned at /admin, seeded, with attackplug granted site.scripts.
import { launch, newOwner, bearer, base, email, password, record, finish } from "./lib.mjs";

const browser = await launch();
const { context } = await newOwner(browser); // admin form login only
const pub = await context.newPage();
await pub.goto(`${base}/about`);
await pub.waitForFunction(() => window.__atk?.executed === true);
const atk = (fn, ...arg) => pub.evaluate(({ fn, arg }) => window.__atk[fn](...arg), { fn, arg });

const token = await bearer();
const formNames = async () => ((await (await fetch(`${base}/api/v1/forms`, { headers: { Authorization: `Bearer ${token}` } })).json()).forms ?? []).map((f) => f.name);
const formsBefore = await formNames();
const newForm = (name) => ({ name, slug: name, fields: [], settings: {} });
const stamp = Date.now().toString(36);

// Attack 1: the admin session alone (no API login yet) must not authenticate the API.
const read = await atk("fetchRaw", "/api/v1/users");
const forgedAdmin = await atk("forgedPost", "/api/v1/forms", "forged-" + stamp, newForm("atk-admin-" + stamp));

// Control, on the cookie path: the API's own login in this browser context.
const login = await context.request.post(`${base}/api/v1/auth/login`, { data: { email, password } });
const loginBody = await login.json();
const apiCookie = (await context.cookies(base)).find((c) => c.httpOnly && c.name !== "s" && c.name !== "__Host-s");
const readCtl = await atk("fetchRaw", "/api/v1/users");
const boundOk = await atk("forgedPost", "/api/v1/forms", loginBody.csrf_token ?? "", newForm("atk-bound-" + stamp));

// Attack 2: with the API cookie present, a script cannot compute the bound token: a forged pair fails.
const forgedApi = await atk("forgedPost", "/api/v1/forms", "forged-" + stamp, newForm("atk-forged-" + stamp));
const names = await formNames();

const controlOk = login.status() === 200 && !!loginBody.csrf_token && !!apiCookie && readCtl.status === 200 && /"users"|"email"/.test(readCtl.body) && boundOk.status >= 200 && boundOk.status < 300 && names.includes("atk-bound-" + stamp);
record("AT-03b", "cookie auth on: the admin session does not open the API and a forged CSRF pair writes nothing", {
  ok: [401, 403].includes(read.status) && !/"email"/.test(read.body) && [401, 403].includes(forgedAdmin.status) && [401, 403].includes(forgedApi.status) && !names.some((n) => n.startsWith("atk-admin-") || n.startsWith("atk-forged-")) && formsBefore.every((n) => names.includes(n)),
  control: controlOk,
  detail: `adminSessionGET=${read.status} adminSessionForgedPOST=${forgedAdmin.status} apiCookieForgedPOST=${forgedApi.status} control: apiCookieGET=${readCtl.status} boundTokenPOST=${boundOk.status}`,
});

await browser.close();
finish();
