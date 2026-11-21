# ADR 0006: Static export

Status: accepted (2026-11-21)

## The rules in plain words

1. The server's own router is the only renderer. A static file is what the
   running site answers for that address, not a second rendering of it.
2. Every page is fetched as an anonymous visitor: no cookie, no session, no
   special headers that change the body.
3. A failed export changes nothing that is published. The previous output
   stays as it was.
4. An export deletes only files that an earlier export wrote, and only after
   every upload has worked.
5. A site with the site access gate on (coming soon, maintenance, password) is
   not exported. The export is refused with a clear message.
6. A default install stays served by Hatamoto. Nothing runs and no setting is
   stored until an owner asks for a static export.

## Context

Some sites want to be served as plain files from a directory behind any web
server, or from an S3-compatible bucket behind a CDN. The public site is
rendered by many handlers (pages, archives, taxonomy, sitemaps, feeds, the
robots, llms and knowledge-graph files, theme assets, fonts, media). A static
export that renders them in a second place would drift from what visitors get.

`hatamoto export` ([EXPORT.md](../EXPORT.md)) is a different thing: an archive
of the content and settings for backup and moving hosts. The older note
[ADMIN_STATIC_EXPORT.md](../ADMIN_STATIC_EXPORT.md) is superseded and kept for
the record.

## Decision drivers

- What is published equals what a visitor gets, byte for byte.
- A bad run never damages a working site.
- Nothing private leaves: drafts, scheduled posts, private files, form uploads,
  the admin address.
- Default behaviour unchanged. No cost for a site that does not use it.
- No new URL scheme, because a public address cannot be taken back.

## Decision

### 1. Render through the router

The exporter lives in `internal/staticexport` and never imports the server.
The server gives it a narrow source: it fetches each path with a GET through
the real router, in process, with a loopback address, no `Accept`, no
`Accept-Encoding` and no cookie, and with a user agent that matches no known
crawler. The request carries the prefetch hint, so the 404 log, the redirect
hit counter and the AI-referral counter do not move, and a context switch keeps
the thousands of requests out of the access log. The body is streamed to a file
while its hash is computed.

The command line builds the server without starting it (no worker, no
listener). A later in-process caller must use the live server it already has.

### 2. The inventory is a union

The list of files is the union of three sources: the paths of the sitemap
plan, a fixed set of root files (`/`, `robots.txt`, `sitemap.xml`,
`sitemap.xsl`, `llms.txt`, `feed.xml`, `atom.xml`, `ads.txt`, `app-ads.txt`,
the root icons, the IndexNow key file when on), and a crawl of every fetched
body (HTML, CSS and XML or text), same origin only. Media come only from
references, never from listing the media library. That is what keeps unused
files and form uploads out.

Never fetched: the admin address, `/auth/`, `/plugin-ui/`, `/-/`, `/api/`
(except the bare knowledge-graph URL), `/preview/`, `/health`, `/ready`,
`/forms/`, `/p/` (except plugin asset files), `/search`, `/locale` and
`/.well-known/`.

Outcomes: a 2xx is a file; a same-origin 3xx becomes a stub and a redirect
line, and its target joins the crawl; a 404 or 410 on a crawled link is a
warning; a 5xx, a transport error or a failed seed fails the run.

### 3. Page files and redirects

An HTML answer at an address with no extension is written as
`<path>/index.html`. Everything else keeps its path. Redirects are written two
ways, because a plain file host cannot be assumed to read one format: a
`_redirects` file (Netlify and Cloudflare Pages format; exact rules first, then
pattern rules in the order the server tries them) and, at each redirect source,
a small HTML page with a meta refresh and a canonical link. The target of a
stub is made absolute. Rules that cannot be translated are skipped with a
warning.

The theme's not-found page is written as `404.html`.

### 4. Known gaps, not hidden

- **Query-string listings.** `?page=N` pages and knowledge-graph query
  variants are skipped, counted and warned about. Writing page N to an invented
  path would fix a public URL scheme before the rewrite of links exists, and
  users bookmark such addresses. Skipping adds no URL and can be undone later.
- **Computed redirects.** The legacy WordPress patterns and the bare-slug
  redirects are computed from entity lookups, so there is no rule list to
  write. A link that is crawled and answers 3xx still gets a stub and a line.
  The export warns once when the WordPress toggle is on.

### 5. Targets

**Directory.** The export is staged in a sibling directory on the same
filesystem and swapped in with renames. The previous output is kept as
`<path>.prev`. If the second rename fails, the first is undone. Any build error
removes the staging directory and leaves the output as it was. A guard refuses
a path that is not absolute, is `/`, is or contains the data or media
directory, or is a non-empty directory with no export marker.

**S3-compatible bucket.** Changed files are uploaded with a Content-Type from
the live response and a Cache-Control by kind (short for pages, feeds and
redirects; long and immutable for fingerprinted theme files and fonts; one day
for the rest). Removed files are deleted last, and only those named in the
previous manifest. The bucket is never listed to decide what to delete. Any
upload error stops the run before any delete. The manifest object is written
last. A target that overlaps the media bucket is refused.

### 6. Storage: settings row, run rows, manifest next to the output

One migration adds a single-row settings table (no row means served; the S3
secret key is sealed with the server secret) and a run table with one row per
export. A partial unique index allows one running export at a time, and a run
that stops sending its heartbeat for five minutes is marked failed so a crash
cannot block later runs. The manifest of what was published (path, hash, size,
type) is a file next to the output, not a database row, so losing the database
cannot make a delete wrong.

## Out of scope

Later changes, not this one:

- Interactive islands that call a live origin, and the CORS and redirect
  behaviour around them.
- Triggers (run on publish, on a schedule), debounce, and the admin Hosting
  page. Until then the export is a command.
- Query-string listings written to static paths.
- Expanding the computed WordPress and bare-slug redirects into lists.
- Deploy APIs of specific hosting vendors, edge plugins and partial
  per-entity rendering.

## Consequences

- A static export is exactly as correct as the live site. A page that renders
  wrong live renders wrong in the export, and a 5xx fails the run.
- The export takes a few seconds per hundred pages, and fetches sitemap
  children slowly when the rate limit asks it to.
- Forms, comments, search and the tracking beacon still post to the live site;
  a purely static host does not run them.
- The output directory holds one marker file. Do not point the export at a
  directory you use for anything else.
- The settings and run tables are new. The previous release never reads them,
  so a rollback is safe.
