// The save endpoint's reply, mapped to what the autosave queue expects. Pure, no imports:
// shared by the page builder and the forms designer.

/**
 * Maps the save endpoint's reply to what createAutosave's save() resolves:
 * {ok, version, doc?} | {conflict, version, message} | {stop, message} | {ok:false, message}.
 */
export function mapSaveReply(status, body = {}) {
  if (status >= 200 && status < 300 && body.ok) return { ok: true, version: body.version, doc: body.doc, status: body.status, note: body.note };
  if (status === 409 && body.conflict) return { conflict: true, version: body.version, message: body.error || "This page was changed somewhere else." };
  if (status === 409 || status === 422) return { stop: true, message: body.error || "The page could not be saved.", code: body.code };
  if (status === 401 || status === 403) return { stop: true, message: "Your session has ended. Reload the page to sign in again." };
  return { ok: false, message: body.error || "The page could not be saved." };
}
