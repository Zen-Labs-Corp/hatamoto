---
title: Request flow
nav_title: Request flow
description: Each stage a request passes through, from the socket to the HTML: middleware, routes, page cache, redirects, address lookup and rendering.
---

This page describes every stage a request passes through inside the server, with the headers, limits and rules of each. To see the stages on your own server first, follow [Follow a page request through the server](how-a-page-is-served.md).

## The big picture

Every request enters the same `http.Server` and the same `ServeMux`. The mux is built once at boot, so there is no router state to lock on the request path. Dynamic behaviour (which content lives at which URL, which plugin owns `/p/forms/...`) is decided inside a small number of fixed catch-all handlers.

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

```text
 browser / crawler / agent
          |
          v
 net/http.Server  (SERVER_* timeouts)
          |
          v
 global middleware, outermost first
   1. security headers      (CSP, nosniff, frame, referrer, HSTS in production)
   2. recovery              (a panic becomes a 500)
   3. request log           (one log line per request)
   4. request id            (X-Request-ID is accepted or generated and echoed)
          |
          v
 ServeMux  (fixed at boot; most specific pattern wins)
   |
   |-- GET  /health, /ready, /media/{rest...}
   |-- /api/v1/...        JSON API: sessions or API keys, CSRF, rate limits
   |-- /api/v1/public/... anonymous JSON (public reads, form submit, agent log ingest)
   |-- /api/v1/auth/login, /api/v1/track   anonymous, rate limited
   |-- /api/v1/p/{plugin}/...  plugin admin routes (auth + CSRF)
   |-- /mcp               MCP endpoint (API key bearer, scoped per tool)
   |-- <admin>/...         admin UI (nonce CSP, CSRF, session)
   |-- /p/{plugin}/...    plugin public routes (rate limited)
   |-- robots.txt, sitemap.xml, llms.txt, feed.xml, atom.xml, /theme/assets/...
   |-- POST /forms/{slug}/submit   (spam pipeline: honeypot, captcha, timing, rules)
   |
   '-- GET /  ,  GET /{file} ,  GET /{rest...}     <- the page catch-alls
              |
              v
        agent capture + AI referral recording (in-process, off the hot path)
              |
              v
        page cache lookup  key = engine hash + path   (no query string, or exactly ?page=N)
          hit -----------------------------------------------> write HTML
          miss
              |
              v
        redirect rules  (exact, wildcard, built-in legacy URL families)
              |  match -> 301 (or the rule's status), done
              v
        URL registry  ->  match path against each content type's pattern
              |
              v
        load published entity  (or archive, section landing page, 404)
              |
              v
        build PageView  (site settings, menus, sections, related, forms)
              |
              v
        theme engine: pick layout, run sections, execute html/template
              |
              v
        write HTML  ->  store in cache unless the page is private or degraded
```

## Step by step

### 1. Server and middleware

`hatamoto serve` creates one `http.Server` with the read, write and idle timeouts from the `SERVER_*` settings (see the [configuration reference](../operate/configuration-reference.md)). The defaults are 30 seconds for `SERVER_READ_TIMEOUT` and `SERVER_WRITE_TIMEOUT` and 120 seconds for `SERVER_IDLE_TIMEOUT`. The handler is the mux wrapped in four middleware, listed outermost first in `buildHandler`:

1. **Security headers.** Sets `X-Content-Type-Options`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy` and a Content-Security-Policy. The policy is sent as `Content-Security-Policy-Report-Only` until `CSP_ENFORCE=true`. HSTS is added only when the environment is `production`. `SECURITY_HEADERS_DISABLED` turns the whole step off for local debugging. Public pages and the admin replace parts of these headers later (see step 8 below and [Auth and security](auth-and-security.md)).
2. **Recovery.** A panic in a handler is caught, logged and answered with a 500.
3. **Request logging.** Each request produces one structured `slog` line, `msg="http request"` (`"msg":"http request"` in the JSON log that `ENVIRONMENT=production` writes), with `method`, `path`, `remote_addr`, `status`, `size`, `duration` and `user_agent`.
4. **Request id.** The `X-Request-ID` request header is reused if present, otherwise a random 32-character hex id is generated. It is set on the response. It is not written to the request log line, because the logger runs outside this step.

Unmatched paths and wrong methods get a small JSON body (`{"error":"not found"}` or `{"error":"method not allowed"}`) instead of the default plain text. When a path has a route under another method, the answer is a 405 with an `Allow` header.

### 2. Picking the route

Go's `ServeMux` chooses the most specific pattern. Fixed routes (`/health`, `/ready`, `<admin>/...`, `/api/v1/...`, `/mcp`, the feeds and `robots.txt`) win over the page catch-alls. A `GET` pattern also serves `HEAD`, so a `HEAD /` never falls through to the wrong handler.

Because `ServeMux` wildcards cover whole path segments, a child sitemap such as `sitemap-posts.xml` cannot be written as its own pattern. The single-segment route `GET /{file}` checks whether the name is a sitemap child and sends it to the sitemap handler; any other name continues to the page handler.

Anything under `/api` or `/media` that no route claims is not a page: the catch-all answers it with the JSON 404 rather than a themed HTML page.

### 3. Capture and cache

Crawler-visible routes (pages, `robots.txt`, `sitemap.xml`, child sitemaps, `llms.txt`, feeds) pass through a capture middleware. It records AI-crawler hits and AI-assistant referrals in memory and hands them to a writer goroutine, so the response is not delayed by the database. See [Worker and background jobs](worker-and-background.md) for the jobs that maintain that data.

The page cache then gets a chance to answer. A cached page is used only when all of these hold:

- the cache is on (`HATAMOTO_PAGE_CACHE_TTL` and the admin cache setting),
- the public base URL is pinned (`PUBLIC_BASE_URL` or the value saved by the setup wizard), because canonical URLs otherwise depend on the request host,
- the URL has no query string, or the query is exactly `page=N` in plain digits with N of 2 or more (a paged listing),
- no plugin page filter is active for the request, and the request is not a preview.

The key combines a hash of the theme engine with the path, so activating another theme never serves stale markup. Any content, menu or site-settings change drops the whole set of rendered pages (the invalidation is coarse on purpose, because pages embed navigation and sibling data). A redirect added after a page was cached still wins: the redirect lookup is repeated cheaply before a cached body is returned.

### 4. Redirects first

On a miss, the path is trimmed of a trailing slash and checked against redirect rules before any content lookup. The order is: exact rules, then wildcard rules, then the built-in legacy URL families when the site has that option enabled. The first hit wins, and the response is a real HTTP redirect (301 unless the rule says 302, 307 or 308). Targets are validated so a rule cannot send visitors off the site by accident. Each redirect response carries `Cache-Control: public, max-age=300`.

### 5. Resolving the path to content

The URL registry is a snapshot of every content type's routing settings (URL pattern, archive path, section pages), refreshed when a schema is created, changed or deleted, and otherwise at most 60 seconds old. Resolution walks the candidates the pattern router produces for the path:

- A pattern such as `/blog/{slug}` yields a slug and a set of types; the published entity is loaded by slug.
- A pattern with `{path}` (for example `/docs/{path}`) splits the URL into an ancestor chain and loads the entity by walking parents.
- Date tokens `{year}`, `{month}`, `{day}` must match the entity's publication date.
- If no entity matches, a page whose slug equals a section prefix is served as that section's landing page.
- Finally, a type's `archive_path` (for example `/blog`) resolves to a listing.

When no content type matches, three listing addresses are tried: `/search` (the site search, rate limited per IP to 2 requests per second with a burst of 30 when `q` is set), `/category/<term>` and `/tag/<term>`, and `/<kind>/<term>` for a custom term kind. A nested term has one address, its ancestors' slugs joined by `/`, at most 8 levels deep.

Only published entities are served. Drafts, scheduled and archived entities return 404 publicly. If nothing resolves, the request is logged in the 404 log (with referrer and user agent, pruned 90 days after it was last seen) and the theme's `404` layout is rendered with status 404 and `Cache-Control: private, no-cache, no-store`.

### 6. Building the view

`buildPageView` turns the resolved entity into a `PageView`: site settings and navigation, the page or section content, forms referenced by sections, internal link targets and a lazily evaluated "related" query. Plugin `render.page` filters can modify the page blocks at this point, and `jsonld.enrich` filters adjust structured data, each under a timeout; see [Plugin runtime](plugin-runtime.md).

### 7. Rendering

The theme engine selects a layout using a fixed hierarchy:

| View | Layouts tried, in order |
|---|---|
| single entity | `single-<type>`, `single`, `page` |
| home | `home`, `page` |
| type archive | `archive-<type>`, `archive`, `page` |
| term archive | `taxonomy-<kind>`, `taxonomy`, `page` |
| author | `author`, `page` |
| search | `search`, `page` |
 Files are resolved through a layer chain with the highest-precedence source first (the active theme package, then the embedded default theme). Each section is rendered through its own template, then the layout executes and writes to a buffer.

If any section fails, a plugin func fails or times out, or a data query degrades, the page is still served but it is marked uncacheable so a transient failure never sticks for the TTL.

### 8. Response

The HTML is written with `Content-Type: text/html; charset=utf-8` (no header names the renderer). A cacheable page gets `Cache-Control: public, max-age=60, s-maxage=300`; a private or degraded one gets `private, no-cache, no-store` and `Vary: Cookie`. Public pages replace two of the global headers: `X-Frame-Options` becomes `SAMEORIGIN` and the policy allows `frame-ancestors 'self'`, so the admin editor can show the page in a frame, and it adds `img-src https:` and `frame-src https:` (plus the origins of the configured captcha provider).

If the page is cacheable (public, not degraded, not a preview), it is stored with the TTL from the admin setting or `HATAMOTO_PAGE_CACHE_TTL` (default 5 minutes; both accept 30 seconds to 30 days). See [Performance and the page cache](../admin/performance-cache.md) for the owner's settings. Writes use a generation check so a page rendered before an invalidation cannot overwrite a newer state.

## The admin and API paths

`<admin>/...` pages skip the page catch-alls entirely. They run behind the admin's own middleware: strict nonce-based CSP, a CSRF check on every non-read request, then a session check that redirects to the sign-in page when there is none. `/api/v1/...` groups apply session or API key authentication once at the group level, with each handler adding role checks. Both are covered in [Auth and security](auth-and-security.md).

You can list the fixed routes with the command below. It needs no database and prints one `METHOD pattern` line per route with the handler's name, about 500 lines in all:

```bash
hatamoto routes
```

Plugin routes are not in that list because they live on per-plugin routers behind the fixed `/p/{plugin}/...` catch-alls.
