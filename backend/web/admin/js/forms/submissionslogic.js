// The Submissions tab's pure logic: row navigation, the keys the tab may take, the drawer URL. No DOM here.

const TYPING = new Set(["INPUT", "TEXTAREA", "SELECT"]);

/** The row index after pressing j (down) or k (up); from no selection (-1) either key lands on the first row. */
export function nextIndex(i, n, key) {
  if (n <= 0) return -1;
  if (i < 0) return 0;
  if (key === "j") return Math.min(i + 1, n - 1);
  if (key === "k") return Math.max(i - 1, 0);
  return i;
}

/** True when a keydown may drive the list: not while typing in a field and not with a shortcut modifier held. */
export function shouldHandleKey(e) {
  if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey) return false;
  const t = e.target;
  if (t && (TYPING.has(t.tagName) || t.isContentEditable)) return false;
  return true;
}

/** The URL that opens one submission: the list URL's q, status and page are kept, s is set to id. */
export function drawerURL(href, id) {
  const u = new URL(href, "http://x.invalid");
  u.searchParams.set("s", String(id));
  return u.pathname + u.search;
}

/** The list URL without the open submission, for when the drawer closes. */
export function listURL(href) {
  const u = new URL(href, "http://x.invalid");
  u.searchParams.delete("s");
  return u.pathname + u.search;
}
