// Saves the whole form definition as JSON (PUT) and keeps the state the status line shows:
// idle|pending|saving|saved|error|conflict|stopped. One save runs at a time (a second request
// waits and runs once after it). 409 and a lost session stop saving until the page is reloaded;
// 422 maps its messages by field id and lets the person fix and save again; a network error
// keeps the changes dirty so the next save retries.
import { mapSaveReply } from "@hm/builder/savereply";

/**
 * onState(state, message), onErrors({[field_id]: message}, summary) (called with {} after a clean save).
 * The definition's slug is never sent.
 */
export function createFormSaver({ url, version, getDoc, onState = () => {}, onErrors = () => {}, fetchImpl = (...a) => fetch(...a), csrf = () => "" }) {
  let state = "idle";
  let dirty = false;
  let seq = 0; // bumped by markDirty: a save only cleans the changes it carried
  let inflight = null;
  let queued = false;
  const set = (s, msg = "") => {
    state = s;
    onState(s, msg);
  };

  async function run() {
    const sent = seq;
    const d = getDoc();
    set("saving");
    let res;
    let body = {};
    try {
      const r = await fetchImpl(url, {
        method: "PUT",
        headers: { "Content-Type": "application/json", Accept: "application/json", "X-CSRF-Token": csrf() },
        body: JSON.stringify({ version, name: d.name, description: d.description ?? "", status: d.status, fields: d.fields.map((f, i) => ({ ...f, position: i })), settings: d.settings ?? {} }),
      });
      body = await r.json().catch(() => ({}));
      res = mapSaveReply(r.status, body);
      if (r.status === 422) res = { invalid: true, message: body.error || "Some things need attention." };
    } catch {
      res = { ok: false, message: "The form could not be saved. Check your connection and try again." };
    }
    if (res.ok) {
      version = res.version ?? version;
      dirty = seq !== sent;
      onErrors({}, "");
      set(dirty ? "pending" : "saved");
    } else if (res.conflict) {
      version = res.version ?? version;
      set("conflict", "This form was changed in another tab. Reload to see the latest.");
    } else if (res.stop) {
      set("stopped", res.message);
    } else if (res.invalid) {
      const map = {};
      for (const e of body.errors ?? []) if (e.field_id && !(e.field_id in map)) map[e.field_id] = e.message;
      onErrors(map, res.message);
      set("error", res.message);
    } else {
      set("error", res.message);
    }
  }

  return {
    /** Starts a save, or queues one behind the running save. Resolves when the request(s) finish. */
    save() {
      if (state === "conflict" || state === "stopped") return Promise.resolve();
      if (inflight) {
        queued = true;
        return inflight;
      }
      inflight = (async () => {
        try {
          do {
            queued = false;
            await run();
          } while (queued && state !== "conflict" && state !== "stopped");
        } finally {
          inflight = null;
        }
      })();
      return inflight;
    },
    get dirty() {
      return dirty;
    },
    get state() {
      return state;
    },
    get version() {
      return version;
    },
    markDirty() {
      dirty = true;
      seq++;
      if (state !== "conflict" && state !== "stopped" && state !== "saving") set("pending");
    },
  };
}
