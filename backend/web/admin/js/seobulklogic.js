// Pure helpers of the inline SEO view (seobulk.js), testable without a DOM:
// scripts/admin-qa/unit/seobulk.test.mjs.

const TEXT = { unsaved: "Unsaved changes", saving: "Saving…", saved: "Saved", error: "Failed: retry", conflict: "Changed elsewhere" };

/** The text a row's save indicator shows for a state. */
export const stateText = (state) => TEXT[state] ?? "";

/** Whether the row offers its Save button, as Retry: after a failed save or a conflict (the user's value is still in the field). */
export const showsRetry = (state) => state === "error" || state === "conflict";

/** Whether any field differs from the copy the row was loaded with. */
export function rowDirty(values, orig) {
  return Object.keys(orig).some((k) => (values[k] ?? "") !== (orig[k] ?? ""));
}

/** The posted values of a row: text fields as typed, the switch as "true" or "". */
export function rowValues(fields) {
  const out = {};
  for (const [name, f] of Object.entries(fields)) out[name] = f.type === "checkbox" ? (f.checked ? "true" : "") : f.value;
  return out;
}

/**
 * What a save reply means for the row: the indicator state and text, the values that
 * become the new loaded copy (orig), and the fields to show again from the server.
 * posted is what the save sent and current what the inputs hold now (both as rowValues):
 * a field the user did not touch since the post shows the server's value again, so input and
 * orig never disagree about a change someone else made; one edited meanwhile keeps the edit.
 * A field that conflicts keeps the user's value too: it is dirty against the new orig, so Retry saves it.
 * The reply is {status, message, values}; a missing or odd reply is an error.
 */
export function applyResponse(httpStatus, json, posted = {}, current = posted) {
  const vals = json?.values;
  const orig = vals ? { title: vals.title ?? "", description: vals.description ?? "", keyword: vals.keyword ?? "", noindex: vals.noindex ? "true" : "" } : null;
  const clash = json?.status === "conflict" ? (json.conflicts ?? {}) : {};
  const refresh = orig ? Object.keys(orig).filter((n) => !(n in clash) && (current[n] ?? "") === (posted[n] ?? "") && orig[n] !== (current[n] ?? "")) : [];
  if (httpStatus === 200 && json?.status === "saved") return { state: "saved", text: stateText("saved"), orig, refresh };
  if (json?.status === "conflict") return { state: "conflict", text: json.message || stateText("conflict"), orig, refresh };
  return { state: "error", text: json?.message || stateText("error"), orig: null, refresh: [] };
}

/** The list URL to go to once when the SEO view was used last and the URL names no view; else null. */
export function viewRedirect(search, stored) {
  const q = new URLSearchParams(search);
  if (stored !== "seo" || q.has("view")) return null;
  q.set("view", "seo");
  return `?${q}`;
}

/** The view to remember for a list URL: "seo" when asked, "" when the URL names another (the Default tab's view=default) or none. */
export const viewToRemember = (search) => (new URLSearchParams(search).get("view") === "seo" ? "seo" : "");
