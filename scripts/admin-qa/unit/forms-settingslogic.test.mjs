// The Settings tab's pure logic. Run: node --test scripts/admin-qa/unit/forms-settingslogic.test.mjs
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
const L = await import(new URL("forms/settingslogic.js", root));
const { createStore, commit, undo, redo } = await import(new URL("forms/ops.js", root));

const fields = [
  { id: "a", type: "text", label: "First name", name: "first_name" },
  { id: "b", type: "email", label: "Email", name: "email" },
];

test("fillTokens fills known fields, drops unknown ones and keeps one line", () => {
  const data = { first_name: "Ada", email: "ada@example.com", secret: "x" };
  assert.equal(L.fillTokens("New enquiry from {{first_name}}", fields, data), "New enquiry from Ada");
  assert.equal(L.fillTokens("Hi {{ first_name }} {{secret}} {{nope}}!", fields, data), "Hi Ada !");
  assert.equal(L.fillTokens("{{first_name}}", fields, { first_name: "Ada\r\nBcc: evil@example.com" }), "Ada Bcc: evil@example.com");
  assert.equal(L.fillTokens("a\nb", fields, {}), "a b");
  assert.equal(L.fillTokens("x".repeat(300), fields, {}).length, 200);
  assert.equal(L.fillTokens(undefined, fields, {}), "");
});

test("recipients: a legacy string reads as a list and an unedited list is not rewritten", () => {
  assert.deepEqual(L.parseStoredRecipients("a@example.com, b@example.com;c@example.com"), ["a@example.com", "b@example.com", "c@example.com"]);
  assert.deepEqual(L.parseStoredRecipients(undefined), []);
  assert.equal(L.joinRecipients(["a@example.com", "b@example.com"]), "a@example.com, b@example.com");
  assert.equal(L.joinRecipients([]), null);
  const legacy = "a@example.com,b@example.com"; // not in the canonical ", " shape
  const st = createStore({ settings: { notify_email: legacy }, fields: [] });
  // the panel only writes on a user change: reading the list back out changes nothing
  assert.equal(st.doc.settings.notify_email, legacy);
  assert.equal(L.settingsPatch("notify_email", L.joinRecipients(["a@example.com"])), "a@example.com");
});

test("settingsPatch: switches are explicit, empty values delete, the trap field is inverted", () => {
  assert.equal(L.settingsPatch("store_submissions", false), false); // never null: absent would mean something else to older readers
  assert.equal(L.settingsPatch("store_submissions", true), true);
  for (const k of ["enable_captcha", "confirmation_email", "no_ip"]) assert.equal(L.settingsPatch(k, false), false);
  assert.equal(L.settingsPatch("honeypot_off", true), null); // trap field on: the key is absent
  assert.equal(L.settingsPatch("honeypot_off", false), true);
  assert.equal(L.settingsPatch("rate_limit_per_hour", ""), null);
  assert.equal(L.settingsPatch("rate_limit_per_hour", " 5 "), 5);
  assert.equal(L.settingsPatch("rate_limit_per_hour", "0"), null);
  assert.equal(L.settingsPatch("rate_limit_per_hour", "five"), undefined);
  assert.equal(L.settingsPatch("rate_limit_per_hour", "-1"), undefined);
  assert.deepEqual(L.settingsPatch("blocked_words", "casino\r\n\n  example.net \n"), ["casino", "example.net"]);
  assert.equal(L.settingsPatch("blocked_words", "  \n"), null);
  assert.equal(L.settingsPatch("retention_days", "90"), 90);
  assert.equal(L.settingsPatch("retention_days", "0"), null);
  assert.equal(L.settingsPatch("retention_days", "7"), undefined);
  assert.equal(L.settingsPatch("notify_subject", ""), null);
  assert.equal(L.settingsPatch("notify_subject", "Hello"), "Hello");
  assert.deepEqual(L.retentionOptions.map((o) => o.days), [30, 90, 365, 0]);
});

test("controlValue is the inverse of settingsPatch", () => {
  const s = { honeypot_off: true, rate_limit_per_hour: 5, blocked_words: ["a", "b"], retention_days: 30, store_submissions: false, notify_email: "a@example.com, b@example.com" };
  assert.equal(L.controlValue("honeypot_off", s), false);
  assert.equal(L.controlValue("honeypot_off", {}), true);
  assert.equal(L.controlValue("rate_limit_per_hour", s), "5");
  assert.equal(L.controlValue("rate_limit_per_hour", {}), "");
  assert.equal(L.controlValue("blocked_words", s), "a\nb");
  assert.equal(L.controlValue("retention_days", s), 30);
  assert.equal(L.controlValue("retention_days", {}), 0);
  assert.equal(L.controlValue("store_submissions", s), false);
  assert.deepEqual(L.controlValue("notify_email", s), ["a@example.com", "b@example.com"]);
  assert.equal(L.controlValue("notify_subject", {}), "");
});

test("whenMatches: setting switches and the screen's own choice", () => {
  assert.equal(L.whenMatches("store_submissions", { store_submissions: true }), true);
  assert.equal(L.whenMatches("store_submissions", { store_submissions: false }), false);
  assert.equal(L.whenMatches("!store_submissions", {}), true);
  assert.equal(L.whenMatches("@after:redirect", {}, { after: "redirect" }), true);
  assert.equal(L.whenMatches("@after:message", {}, { after: "redirect" }), false);
});

test("confirmationPreview uses the sample's address, the stored text and the defaults", () => {
  const doc = { name: "Contact", fields, settings: {} };
  assert.deepEqual(L.confirmationPreview(doc, { email: "ada@example.com" }), { to: "ada@example.com", subject: "We received your submission — Contact", message: "Thank you. We have received your submission." });
  const set = { ...doc, settings: { confirmation_subject: "Got\n it", confirmation_message: " Thanks! ", success_message: "Sent" } };
  assert.deepEqual(L.confirmationPreview(set, null), { to: "visitor@example.com", subject: "Got it", message: "Thanks!" });
  assert.equal(L.confirmationPreview({ ...doc, settings: { success_message: "Sent" } }, null).message, "Sent");
  assert.deepEqual(L.sampleData(fields, null), { first_name: "First name", email: "Email" });
  assert.deepEqual(L.sampleData(fields, { first_name: "Ada" }), { first_name: "Ada" });
});

test("typing in a settings control is one undo step; undo and redo restore the value", () => {
  let st = createStore({ name: "F", status: "active", fields: [], settings: {} });
  for (const v of ["a", "ab", "abc"]) st = commit(st, [{ op: "setSettings", key: "notify_subject", value: v }], { coalesce: "settings:notify_subject" });
  st = commit(st, [{ op: "setSettings", key: "no_ip", value: true }], {});
  assert.equal(st.past.length, 2);
  st = undo(st);
  assert.equal(st.doc.settings.no_ip, undefined);
  st = undo(st);
  assert.equal(st.doc.settings.notify_subject, undefined);
  st = redo(st);
  assert.equal(st.doc.settings.notify_subject, "abc");
});
