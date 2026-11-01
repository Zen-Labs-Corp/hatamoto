// The builder's server sync: the canvas render queue and the save/autosave/recovery
// side. Neither knows the DOM; fetch, canvas and timers are injected so the
// queue and the response mapping are unit-tested with fakes.
//
// Renderer: one commit = one job on a serial chain. A job asks the server to apply the
// commit's ops to the doc as it was BEFORE the commit and render the nodes the bridge
// plan named (stateless server, nothing stored), then posts the plan's messages to the
// canvas in order. Anything the plan cannot express, a failed request or a refused
// patch falls back to staging the whole doc again (the self-heal).
//
// Saver: drafts autosave (createAutosave); a published page only keeps a recovery copy
// on the server until the person presses Update. Every save carries the version it was opened
// at, so another editor's save is a conflict, never an overwrite.
import { planCommit, finish, reanchor } from "@hm/builder/bridge";
import { find, locate, v1Shaped } from "@hm/builder/store";
import { createAutosave, createRecovery, jsonPost, recoveryAction } from "@hm/editor/autosave";

const MAX_OPS = 100; // the render endpoint's limits
const MAX_IDS = 50;

/** The key the canvas gives the fragment of node id: a v1-shaped top-level container is shown as its component. */
export function fragmentKey(doc, id) {
  const l = locate(doc, id);
  return l && !l.parent && v1Shaped(l.node) ? l.node.c[0].id : id;
}

/**
 * createRenderer({urls, csrf, canvas, getDoc, fetch, onError, decls}) -> {stage, commit}
 * urls: {stage, render}; canvas: the createCanvas controller; getDoc(): the current doc.
 */
export function createRenderer({ urls, csrf = () => "", canvas, getDoc, fetch: f = (...a) => globalThis.fetch(...a), onError = () => {}, decls }) {
  let rev = 0;
  let stageSeq = 0;
  let staging = false;
  let restage = false;
  let chain = Promise.resolve();
  let seq = 0;
  let gen = 0; // bumped by every stage: a queued job planned against an older frame must not patch the new one

  /** Loads the doc into the canvas as a fresh page (first load, self-heal). */
  async function stage(doc = getDoc()) {
    gen++;
    if (staging) {
      restage = true;
      return;
    }
    staging = true;
    const mine = ++stageSeq;
    try {
      const res = await f(urls.stage, jsonPost(csrf, { doc }));
      if (!res.ok) throw new Error(`stage ${res.status}`);
      const { url } = await res.json();
      if (mine === stageSeq) {
        canvas.load(url, ++rev);
        // the frame shows `doc`: an edit made while the request was in flight is not in it
        if (getDoc() !== doc) restage = true;
      }
    } catch (err) {
      onError("The page preview could not be loaded.", err);
    } finally {
      staging = false;
      if (restage) {
        restage = false;
        stage(getDoc());
      }
    }
  }

  async function job(before, after, ops, plan, born) {
    if (born !== gen) return;
    const needServer = plan.render.length > 0 || plan.css.length > 0;
    let entries = plan.messages;
    if (!needServer) {
      if (!canvas.apply(entries.map((e) => e.msg))) await stage(getDoc());
      return;
    }
    if (plan.render.length === 0) {
      // a style change: preview at once, the server confirms the compiled rules after
      const now = entries.filter((e) => e.when === "now");
      if (now.length && !canvas.apply(now.map((e) => e.msg))) return stage(getDoc());
      entries = entries.filter((e) => e.when !== "now");
    }
    const res = await f(urls.render, jsonPost(csrf, { doc: before, ops, render: plan.render, css: plan.css, seq: ++seq }));
    if (!res.ok) throw new Error(`render ${res.status}`);
    const body = await res.json();
    if (born !== gen) return; // a stage began while the render was in flight
    const byKey = new Map((body.fragments ?? []).map((fr) => [fr.key, fr]));
    const frags = plan.render.map((id) => byKey.get(fragmentKey(after, id)));
    // an inserted node that renders nothing (a blank section) is simply not in the frame, as in a fresh render;
    // any other op on a node that renders nothing shows the server's truth
    const absent = (e) => e.when === "fragment" && !frags[e.frag];
    if (entries.some((e) => absent(e) && e.msg.type !== "insert")) return stage(getDoc());
    const order = Array.isArray(body.order) ? body.order : null;
    const skip = new Set(plan.render.map((id) => fragmentKey(after, id)));
    const msgs = entries
      .filter((e) => !absent(e))
      .map((e) => finish(e, { frags, css: body.css ?? {} }))
      .map((m) => (order ? reanchor(after, order, m, skip) : m));
    if (msgs.length && !canvas.apply(msgs)) return stage(getDoc());
    // the frame only counts sections (components and v1-shaped containers) in its order
    if (Array.isArray(body.order)) {
      const sections = new Set(after.nodes.filter((n) => n.t === "component" || v1Shaped(n)).map((n) => fragmentKey(after, n.id)));
      canvas.expectOrder(body.order.filter((k) => sections.has(k)));
    }
  }

  /**
   * One commit: before/after docs and the ops that turn one into the other.
   * inline: a text change already in the frame's DOM (typing on the canvas).
   */
  function commit(before, after, ops, { inline = false } = {}) {
    const plan = planCommit(before, after, ops, { inline, decls });
    if (plan.reload || ops.length > MAX_OPS || plan.render.length > MAX_IDS) return stage(after);
    if (plan.messages.length === 0) {
      // typing on the canvas is already in its DOM; a frame that is still loading will not have it
      return inline && !canvas.apply([]) ? stage(getDoc()) : Promise.resolve();
    }
    const born = gen;
    chain = chain.then(() => job(before, after, ops, plan, born)).catch((err) => {
      onError("The page preview fell behind and is reloading.", err);
      return stage(getDoc());
    });
    return chain;
  }

  return { stage, commit, idle: () => chain };
}

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

/** What the bar adds while a published page has unsaved changes, by the state of the server copy. */
const KEEP_LINES = {
  kept: "Draft saved · not live yet",
  failed: "Not saved. You appear to be offline.",
  expired: "Your session ended: sign in again in another tab.",
  refused: "Unsaved changes, not kept. Update the page to save them.",
};

/** The few words the bar keeps on a phone ("Draft · Saved"): no reason (the full line stays for a screen reader). */
export const shortLine = (q, status = "") => ({ pending: status === "Published" && !q.keep ? "Saving…" : "Not saved", saving: "Saving…", saved: `${status ? status + " · " : ""}Saved`, error: "Not saved", conflict: "Changed elsewhere", stopped: "Not saved" })[q.state] ?? "";

/** The save-state line shown in the bar. status: "Draft" or "Published" (empty for a block or part). The time of the last save is savedTime(), for the title. */
export function stateLine(q, { draft, status = "" } = {}) {
  switch (q.state) {
    case "pending":
      if (draft) return "Not saved";
      return KEEP_LINES[q.keep] ?? (status === "Published" ? "Saving…" : "Unsaved changes"); // the recovery copy is on its way
    case "saving":
      return "Saving…";
    case "saved":
      return status === "Draft" ? "Draft saved · not live yet" : status ? `${status} · Saved` : "Saved";
    case "error":
      return `${q.message || "The page could not be saved."} Retrying…`;
    case "conflict":
      return "Changed somewhere else";
    case "stopped":
      return q.message || "Saving stopped";
    default:
      return "";
  }
}

/** The title of the save-state element: when the last save happened ("Saved at 14:02"), empty before one. */
export const savedTime = (at) => (at ? `Saved at ${at}` : "");

/**
 * Whether the page carries changes the public copy does not have: only a published page can (a draft page is all draft), while the
 * state is anything but saved and there are edits (kept as a draft on the server, or still held here). It clears on saved.
 */
export const updateState = (q, { status = "", dirty = false } = {}) => ({
  unpublished: status === "Published" && !["saved", "idle", undefined].includes(q.state) && (q.keep === "kept" || !!dirty),
});

/**
 * createSaver({urls, csrf, boot, getDoc, getName, getSEO, onState, onAdopt, fetch, timers, delay})
 * boot: {entity, status, version, name, doc, recovery?}; recovery is the copy the server kept: {content, version, name, at}. onState(q): q.state is
 * idle|pending|saving|saved|error|conflict|stopped. onAdopt(doc): the server stored a
 * different doc (its sanitiser), the client takes it. Returns {changed, save, resolve,
 * restorable, discardLocal, dirty, flushLocal, q}; q.keep says whether the server holds the latest unsaved copy.
 */
export function createSaver({ urls, csrf = () => "", boot, getDoc, getName = () => boot.name, getSEO = () => null, onState = () => {}, onAdopt = () => {}, fetch: f = (...a) => globalThis.fetch(...a), timers, delay = 3000 }) {
  // A block or layout part is saved explicitly (it is live in every page that uses it): no autosave.
  const manual = boot.reuse?.role === "block" || boot.reuse?.role === "part";
  const draft = boot.status === "draft" && !manual;
  const t = timers ?? { set: (fn, ms) => setTimeout(fn, ms), clear: (id) => clearTimeout(id) };
  let version = boot.version;
  let edits = 0; // bumped by every change, so a save knows whether more happened since it started
  let unsaved = false;
  let explicit = null; // the explicit save a conflict interrupted
  const q = { state: "idle", message: "", code: "", auto: false, conflict: null, keep: "" }; // code: the refusal a stopped save carries; auto: that save was a background autosave
  const b = boot.recovery;
  const recovery = createRecovery({
    url: urls.recovery,
    discardUrl: urls.recoveryDiscard,
    csrf,
    copy: b && typeof b.content === "string" ? { content: b.content, version: Number(b.version) || 0, name: typeof b.name === "string" ? b.name : "", at: typeof b.at === "string" ? b.at : "" } : null,
    fetch: f,
    timers: t,
    onState: (s) => {
      q.keep = s === "idle" ? "" : s;
      if (q.state === "pending") onState(q);
    },
  });
  const set = (state, message = "") => {
    q.state = state;
    q.message = message;
    onState(q);
  };

  // The SEO values travel only once the drawer changed a field: a save without them leaves the stored ones alone.
  const seoMember = () => {
    const seo = getSEO();
    return seo ? { seo } : {};
  };

  async function post(op, v) {
    const sent = edits;
    let res;
    const own = op !== "autosave";
    if (own) recovery.suspend(); // a copy written during the save could land after it and bring the old doc back
    try {
      const r = await f(urls.save, jsonPost(csrf, { version: v, doc: getDoc(), op, name: getName(), ...seoMember() }));
      res = mapSaveReply(r.status, await r.json().catch(() => ({})));
      if (res.code === "invalid_page_doc") res.message = (await import("@hm/builder/elementsettings")).nameNodes(res.message, getDoc()); // loaded only to explain a refusal
    } catch (err) {
      res = { ok: false, message: err instanceof TypeError ? "Not saved. You appear to be offline." : err?.message || "The page could not be saved." };
    }
    q.code = res.stop ? res.code || "" : "";
    q.auto = !own;
    if (res.ok) {
      version = res.version;
      if (sent === edits) {
        recovery.clear(); // also drops a pending write, which would resurrect the page just saved
        q.keep = "";
        unsaved = false;
      }
      // edits made while this save was in flight stay: the next save sends them and its reply is adopted once idle
      if (res.doc && sent === edits) onAdopt(res.doc);
    }
    if (own && !res.ok) recovery.resume();
    if (own && res.ok && sent !== edits) recovery.resume(); // clear() did not run: the edits typed meanwhile still want their copy
    return res;
  }

  const autosave = draft
    ? createAutosave({
        save: async ({ version: v }) => post("autosave", v),
        version,
        timers: t,
        delay,
        onState: (s) => {
          q.conflict = s.conflict;
          set(s.state, s.message);
        },
        onSaved: (v) => {
          version = v;
        },
      })
    : null;

  return {
    q,
    get draft() {
      return draft;
    },
    get version() {
      return version;
    },
    get dirty() {
      return unsaved || (autosave?.dirty ?? false);
    },
    /** The doc or the title changed. */
    changed() {
      edits++;
      unsaved = true;
      if (q.keep === "kept") q.keep = "";
      if (!draft) recovery.write(() => ({ content: JSON.stringify(getDoc()), version, name: getName() })); // a draft has its autosave
      if (autosave) {
        autosave.resume();
        autosave.change();
      } else set("pending");
    },
    /** Sends the pending copy now (the tab is going away). */
    flushLocal() {
      recovery.flush();
    },
    /** Explicit save: op is "save" or "publish". Resolves the mapped reply. */
    async save(op) {
      explicit = op;
      if (autosave) {
        await autosave.flush();
        if (q.state === "conflict") return { conflict: true, version: q.conflict?.version };
      }
      set("saving");
      const res = await post(op, autosave ? autosave.q.version : version);
      if (autosave && res.ok) autosave.q.version = res.version;
      if (!res.conflict) explicit = null;
      if (!res.ok) recovery.flush(false); // what could not be saved is at least kept on the server
      if (res.ok) {
        set(!autosave && unsaved ? "pending" : "saved"); // edits typed during an Update are not saved yet
      } else if (res.conflict) {
        q.conflict = { version: res.version, message: res.message };
        set("conflict", res.message);
      } else if (res.stop) {
        set("stopped", res.message);
      } else {
        set("error", res.message);
      }
      return res;
    },
    /** After a conflict: "overwrite" saves the person's doc over the stored version. */
    async resolve(choice) {
      const v = q.conflict?.version;
      q.conflict = null;
      if (choice !== "overwrite") {
        set("idle");
        return null;
      }
      version = v;
      if (autosave?.q.state === "conflict") autosave.resolve("overwrite", v);
      else if (autosave) autosave.q.version = v;
      return explicit ? this.save(explicit) : null;
    },
    /** The server-kept copy when it differs from the page: {copy, action: "restore"|"stale"} or null. */
    restorable() {
      const copy = recovery.copy;
      const action = recoveryAction(copy, { content: JSON.stringify(boot.doc), version, name: boot.name });
      return action === "none" ? null : { copy, action };
    },
    discardLocal() {
      recovery.clear();
    },
    /** "Load their version": nothing here is worth keeping, so the reload neither warns (not dirty) nor sends the server copy again; clear() deletes it. */
    leave() {
      unsaved = false;
      recovery.clear();
      autosave?.stop(); // not awaited: the reload must not wait for a request in flight
    },
    destroy() {
      autosave?.destroy();
    },
  };
}
