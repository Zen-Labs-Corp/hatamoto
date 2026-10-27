// Link policy shared by every editor (port of entity-link.ts helpers plus the
// URL validation the link dialog applies). Pure; no Tiptap, no DOM.

const SAFE_SCHEMES = new Set(["http:", "https:", "mailto:", "tel:"]);

/**
 * Validates what a person typed into the link field. Returns {ok:true, href}
 * (trimmed) or {ok:false, error}. Relative paths, fragments and queries are
 * fine; only http(s), mailto and tel carry a scheme; javascript:, data: and
 * friends are refused, also when disguised with whitespace or control bytes.
 */
export function validateLinkURL(raw) {
  const href = String(raw ?? "").trim();
  if (href === "") return { ok: false, error: "Enter an address." };
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f\s]/.test(href)) return { ok: false, error: "The address cannot contain spaces." };
  if (href.startsWith("//") || href.startsWith("/\\")) return { ok: false, error: "Start with https:// or a path starting with /." };
  if (/^[#?/]/.test(href)) return { ok: true, href };
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(href);
  if (scheme) {
    return SAFE_SCHEMES.has(`${scheme[1].toLowerCase()}:`)
      ? { ok: true, href }
      : { ok: false, error: "Use an http, https, mailto or tel address." };
  }
  // No scheme: "example.com/x" is a relative path to a browser, so ask for what they meant.
  if (/^[^/]+\.[a-z]{2,}(\/|$)/i.test(href)) return { ok: false, error: "Start with https:// for another site, or with / for a page on this site." };
  return { ok: true, href };
}

/** www/apex twins of a site URL's host ("https://example.com" gives example.com and www.example.com). */
export function siteHostsFrom(siteUrl) {
  if (typeof siteUrl !== "string" || siteUrl.trim() === "") return [];
  try {
    const u = new URL(siteUrl.trim());
    if (u.protocol !== "http:" && u.protocol !== "https:") return [];
    const host = u.host.toLowerCase();
    const name = u.hostname.toLowerCase();
    if (!hasWwwTwin(name)) return [host];
    const port = u.port ? `:${u.port}` : "";
    const twin = name.startsWith("www.") ? name.slice(4) : `www.${name}`;
    return [host, `${twin}${port}`];
  } catch {
    return [];
  }
}

function hasWwwTwin(hostname) {
  if (hostname.startsWith("[")) return false;
  if (/^\d+(\.\d+){3}$/.test(hostname)) return false;
  const apex = hostname.startsWith("www.") ? hostname.slice(4) : hostname;
  return apex.includes(".") && !apex.startsWith(".") && !apex.endsWith(".");
}

/** True for links that stay on this site: relative hrefs, the admin's own origin, siteHosts, entity links. */
export function isInternalHref(rawHref, entityId, siteHosts = [], origin = globalThis.location?.origin) {
  if (entityId) return true;
  if (typeof rawHref !== "string") return false;
  const href = rawHref.trim();
  if (href === "") return false;
  if (href.startsWith("//") || href.startsWith("/\\")) return onSite(href, siteHosts, origin);
  if (/^[#?/]/.test(href)) return true;
  if (!/^[a-z][a-z0-9+.-]*:/i.test(href)) return true;
  return onSite(href, siteHosts, origin);
}

function onSite(href, siteHosts, origin) {
  if (origin) {
    try {
      if (new URL(href, origin).origin === origin) return true;
    } catch {
      return false;
    }
  }
  if (siteHosts.length === 0) return false;
  try {
    const u = new URL(href, "https://invalid.invalid");
    return (u.protocol === "https:" || u.protocol === "http:") && siteHosts.includes(u.host.toLowerCase());
  } catch {
    return false;
  }
}

const SEO_TOKENS = new Set(["nofollow", "noopener", "noreferrer"]);

/**
 * target/rel policy: internal links carry no target=_blank and no
 * nofollow/noopener/noreferrer; external links open in a new tab with
 * noopener noreferrer, keeping any other stored rel token.
 */
export function siteLinkAttrs(attrs, siteHosts = [], origin) {
  const out = { ...attrs };
  const tokens = String(out.rel ?? "").split(/\s+/).filter(Boolean);
  if (isInternalHref(out.href, out["data-entity-id"], siteHosts, origin)) {
    const keep = tokens.filter((t) => !SEO_TOKENS.has(t.toLowerCase()));
    out.rel = keep.length ? keep.join(" ") : null;
    if (out.target === "_blank") out.target = null;
  } else if (out.href) {
    const set = new Set(tokens);
    set.add("noopener");
    set.add("noreferrer");
    out.rel = Array.from(set).join(" ");
    out.target = out.target ?? "_blank";
  }
  return out;
}
