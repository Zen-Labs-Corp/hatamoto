// The Add tab's Recent row (a lazy chunk): the last few items a person inserted, kept per user in
// localStorage. Storage can be full or blocked; then there is simply no Recent row.
const KEY = "hm-builder-recent:";
export const MAX = 8;

/** The stored keys for a user, or [] when there are none or storage fails. */
export function load(user = "", storage = globalThis.localStorage) {
  try {
    const v = JSON.parse(storage?.getItem(KEY + user) ?? "null");
    return Array.isArray(v) ? v.filter((k) => typeof k === "string").slice(0, MAX) : [];
  } catch {
    return [];
  }
}

/** list with key first (once), at most max long. */
export const push = (list, key, max = MAX) => [key, ...list.filter((k) => k !== key)].slice(0, max);

export function save(user, list, storage = globalThis.localStorage) {
  try {
    storage?.setItem(KEY + user, JSON.stringify(list));
  } catch {
    // the row just does not persist
  }
}

/** Remembers key as the latest insert for user and returns the new list. It re-reads storage first, so the Add tab and Ctrl+K (which both call this) never overwrite each other's inserts. */
export function use(user, key, storage = globalThis.localStorage) {
  const list = push(load(user, storage), key);
  save(user, list, storage);
  return list;
}
