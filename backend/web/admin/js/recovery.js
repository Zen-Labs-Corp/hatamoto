// Older versions kept unsaved copies of posts in this browser. They are deleted on
// every admin load and never read; the copies now live on the server. This tiny
// module is part of the admin entry so every admin page cleans up.
export const KEY_PREFIX = "hm:blog:";

/** Removes every old copy from the store. A blocked store has nothing to remove. */
export function purgeLegacyCopies(storage) {
  try {
    const s = storage ?? globalThis.localStorage;
    for (const k of Object.keys(s)) if (k.startsWith(KEY_PREFIX)) s.removeItem(k);
  } catch {
    /* nothing to do */
  }
}

purgeLegacyCopies();
