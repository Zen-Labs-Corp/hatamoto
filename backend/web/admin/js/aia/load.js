// Fetch cache for the AI analytics charts: one request per URL however many
// charts on the page read it (the overview hero, KPI sparks and share chart
// share overview.json). A failed request is dropped so Retry asks again.
const cache = new Map();

export function fetchJSON(url) {
  if (!cache.has(url)) {
    const p = fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin" }).then((r) => {
      if (!r.ok) throw new Error(String(r.status));
      return r.json();
    });
    p.catch(() => {
      if (cache.get(url) === p) cache.delete(url);
    });
    cache.set(url, p);
  }
  return cache.get(url);
}

/** Drop a cached URL (Retry). */
export function forget(url) {
  cache.delete(url);
}
