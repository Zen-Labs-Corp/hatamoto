// Autosave for drafts and the server-kept recovery copy for everything.
//
// createAutosave is a small queue around one async save(): changes are debounced,
// one request is in flight at a time, a network error is retried with backoff,
// and a version conflict stops the queue until the person decides (resolve)
// instead of overwriting somebody else's save. It knows nothing about forms or
// fetch, so it is tested with a fake clock and a fake save.
//
// Cadence: a debounce (`delay` after the last change), at least `minGap` between
// saves, at most `maxWait` of typing without one. Changes during a save coalesce.
//
// save({version}) resolves {ok:true, version} | {conflict:true, version, message}
// | {stop:true, message, code?} (saving can never work: stop and keep the text dirty)
// and rejects (or resolves {ok:false, message}) when the request failed.

export function createAutosave({
  save,
  version,
  delay = 3000,
  minGap = 10000,
  maxWait = 30000,
  retry = [2000, 5000, 15000],
  timers = { set: (f, ms) => setTimeout(f, ms), clear: (t) => clearTimeout(t) },
  now = () => Date.now(),
  onState = () => {},
  onSaved = () => {},
}) {
  const q = {
    version,
    state: "idle", // idle | pending | saving | saved | error | conflict | stopped
    message: "",
    conflict: null,
  };
  let timer = null;
  let dirtySince = 0;
  let lastStart = -Infinity;
  let dirty = false;
  let inflight = null; // the running save's promise
  let stopped = false;
  let failures = 0;
  let stopCode;

  const set = (state, message = "") => {
    q.state = state;
    q.message = message;
    onState(q);
  };
  const clear = () => {
    if (timer !== null) timers.clear(timer);
    timer = null;
  };
  const schedule = (ms) => {
    clear();
    timer = timers.set(() => {
      timer = null;
      run();
    }, ms);
  };

  /** When the next save may start: after the pause, within maxWait, not before minGap. */
  const waitMs = () => {
    const t = now();
    return Math.max(0, Math.min(delay, dirtySince + maxWait - t), lastStart + minGap - t);
  };

  function run() {
    clear();
    if (inflight || !dirty || stopped || q.state === "conflict" || q.state === "stopped") return inflight ?? Promise.resolve();
    inflight = attempt().finally(() => {
      inflight = null;
    });
    return inflight;
  }

  async function attempt() {
    dirty = false;
    dirtySince = 0;
    lastStart = now();
    set("saving");
    let res;
    try {
      res = await save({ version: q.version });
    } catch (err) {
      res = { ok: false, message: err?.message || "The draft could not be saved." };
    }
    if (res.stop) {
      dirty = true;
      stopCode = res.code;
      set("stopped", res.message || "");
      return;
    }
    if (res.conflict) {
      dirty = true;
      q.conflict = { version: res.version, message: res.message || "" };
      set("conflict", res.message || "");
      return;
    }
    if (!res.ok) {
      dirty = true;
      const wait = retry[Math.min(failures, retry.length - 1)];
      failures++;
      set("error", res.message || "The draft could not be saved.");
      if (failures <= retry.length && !stopped) schedule(wait);
      return;
    }
    failures = 0;
    q.version = res.version;
    onSaved(q.version);
    if (dirty && !stopped) {
      set("pending");
      schedule(waitMs()); // typed while saving: one follow-up save
    } else {
      set("saved");
    }
  }

  return {
    q,
    /** Something changed: save after a pause, never later than maxWait. */
    change() {
      if (stopped || q.state === "conflict" || q.state === "stopped") return;
      dirty = true;
      if (!dirtySince) dirtySince = now();
      if (inflight) return;
      failures = 0;
      set("pending");
      schedule(waitMs());
    },
    /** Only an invalid_page_doc stop resumes. */
    resume() {
      if (q.state === "stopped" && stopCode === "invalid_page_doc") q.state = "idle";
    },
    /** The form is submitted: no more saves; resolves when the one in flight lands. */
    async stop() {
      stopped = true;
      dirty = false;
      clear();
      await inflight;
    },
    /** Save now (Ctrl+S, leaving the page). */
    flush: run,
    /** After a conflict: "overwrite" saves the person's text over version v; "discard" gives up the queue. */
    resolve(choice, v = q.conflict?.version) {
      q.conflict = null;
      if (choice === "overwrite") {
        q.version = v;
        set("idle");
        dirty = true;
        dirtySince = now();
        schedule(0);
      } else {
        dirty = false;
        clear();
        q.version = v ?? q.version;
        set("idle");
      }
    },
    /** A save request is in flight. */
    get busy() {
      return inflight !== null;
    },
    get dirty() {
      return dirty || inflight !== null;
    },
    destroy: clear,
  };
}

/** A JSON POST carrying the session's CSRF token (the one request shape the editors' JSON endpoints take). */
export const jsonPost = (csrf, body) => ({
  method: "POST",
  credentials: "same-origin",
  headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() },
  body: body === undefined ? "{}" : JSON.stringify(body),
});
const KEEPALIVE_MAX = 60000; // bytes: browsers drop keepalive bodies above 64 KiB
const RETRY_MAX = 30000;
const byteLength = (s) => new TextEncoder().encode(s).length;

/**
 * The server-kept recovery copy of one entity for the signed-in user. write(copy) is
 * debounced (a function is called when it is sent, so a big doc is not serialised on every change);
 * flush() sends the pending copy at once (keepalive: the tab may be going away);
 * clear() deletes the server copy. `copy` is what the page was delivered, or null.
 * suspend() holds writes while the person's own save is under way (a write landing after the
 * save would bring back a copy of what was just saved); resume() lets them go on.
 * A write that failed is retried with a growing delay and when the browser comes back online.
 * state: "idle" | "kept" (the server has the latest copy) | "failed" (not reached: the text
 * lives in this page only, a retry is queued) | "expired" (the session ended) | "refused" (the
 * server said no). onState() follows it.
 */
export function createRecovery({ url, discardUrl, csrf = () => "", copy = null, fetch: f = (...a) => globalThis.fetch(...a), timers, delay = 1500, onState = () => {}, onOnline = (fn) => globalThis.addEventListener?.("online", fn) }) {
  const t = timers ?? { set: (fn, ms) => setTimeout(fn, ms), clear: (id) => clearTimeout(id) };
  let pending = null;
  let timer = null;
  let held = copy !== null; // the server may hold a copy of ours
  let seq = 0;
  let suspended = false;
  let failures = 0;
  const stop = () => {
    if (timer !== null) t.clear(timer);
    timer = null;
  };
  const arm = (ms) => {
    stop();
    timer = t.set(() => {
      timer = null;
      const sending = prepare(false);
      if (sending) send(sending);
    }, ms);
  };
  const api = {
    copy,
    state: "idle",
    get pending() {
      return pending !== null;
    },
    write(c) {
      pending = c;
      if (suspended) return stop();
      arm(delay);
    },
    flush(keepalive = true) {
      if (pending === null || suspended) return;
      const sending = prepare(keepalive);
      if (!sending) return; // too big for the unload: the timer stays, so "Stay" still sends it
      stop();
      send(sending);
    },
    suspend() {
      suspended = true;
      stop();
    },
    resume() {
      suspended = false;
      if (pending !== null) arm(delay);
    },
    async clear() {
      stop();
      pending = null;
      suspended = false;
      failures = 0;
      seq++; // a write still in flight must not mark the cleared copy as kept
      if (!held) return;
      held = false;
      try {
        await f(discardUrl, jsonPost(csrf));
      } catch {
        /* the copy ages out on the server */
      }
    },
  };
  const set = (state) => {
    api.state = state;
    onState(state);
  };
  function prepare(keepalive) {
    const c = pending;
    const body = jsonPost(csrf, typeof c === "function" ? c() : c);
    if (keepalive) {
      if (byteLength(body.body) > KEEPALIVE_MAX) return null;
      body.keepalive = true;
    }
    return { c, body };
  }
  async function send({ c, body }) {
    const mine = ++seq;
    held = true;
    pending = null;
    try {
      const r = await f(url, body);
      if (mine !== seq) return;
      if (r.ok) {
        failures = 0;
        return set("kept");
      }
      if (r.status === 401 || r.status === 403) return set("expired"); // retrying cannot help until the person signs in
      if (r.status < 500) return set("refused");
      retry(c);
    } catch {
      if (mine !== seq) return;
      retry(c);
    }
  }
  function retry(c) {
    pending ??= c;
    set("failed");
    if (!suspended && timer === null) arm(Math.min(delay * 2 ** failures++, RETRY_MAX));
  }
  onOnline(() => {
    if (pending === null || suspended) return;
    failures = 0;
    arm(0);
  });
  return api;
}

/**
 * What to do with a stored copy against the text the page opened with:
 * "none" (no copy or nothing different), "restore" (a newer copy of this
 * version), "stale" (typed against an older version: the post changed since).
 */
export function recoveryAction(copy, current, norm = (html) => html) {
  if (!copy) return "none";
  // A copy that was submitted and whose post has moved on was saved.
  if (copy.submitted && Number(current.version) > Number(copy.version)) return "none";
  const same = norm(copy.content ?? "") === norm(current.content ?? "") && (copy.name ?? "") === (current.name ?? "") && (copy.excerpt ?? "") === (current.excerpt ?? "");
  if (same) return "none";
  return Number(copy.version) === Number(current.version) ? "restore" : "stale";
}
