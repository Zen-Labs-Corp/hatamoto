// The admin address prefix. The server publishes it as <meta name="admin-base">;
// without the meta (or a document) the default "/admin" applies. Read lazily,
// never at module load, so a module imported before the page is ready still works.

/** The admin prefix, no trailing slash. */
export function adminBase() {
  const v = globalThis.document?.querySelector?.('meta[name="admin-base"]')?.content;
  return (v || "/admin").replace(/\/+$/, "");
}

/** An admin URL: p starts with "/", "?" or "#". */
export const adminURL = (p = "") => adminBase() + p;
