# Static export

> The design and its reasons: [ADR 0006](adr/0006-static-export.md).

`hatamoto static export` renders the public site to plain files: HTML pages,
feeds, sitemaps, theme files and media. You can serve them from any web server
or from an S3-compatible bucket behind a CDN. It is a core feature of every
install and is off until you run it.

This is not the other export commands:

- `hatamoto export` makes an archive of content and settings for backup and
  moving hosts. See [EXPORT.md](EXPORT.md).
- [ADMIN_STATIC_EXPORT.md](ADMIN_STATIC_EXPORT.md) is a superseded note, kept
  for the record.

The admin Hosting page and automatic triggers (run on publish or on a
schedule) come in a later change. For now you run the command yourself.

## Quick start

```
hatamoto static export --out /srv/site-static
static: 214 pages, 61 assets, 388 media files  (38.2 MB)
static: changed 12, unchanged 651, removed 3
static: wrote _redirects (41 rules) + 41 redirect stubs, 404.html
static: swapped /srv/site-static (previous kept at /srv/site-static.prev)
static: done in 9.1s, 0 errors
```

The site address must be set (the `PUBLIC_BASE_URL` setting or Site settings),
for example `https://example.com`. The flags and exit codes are in
[CLI.md](CLI.md#static-export).

## How it works

The export asks the server's own router for each address, as an anonymous
visitor, in process. Each file is therefore what a visitor gets today, byte for
byte. Nothing in the export counts as traffic: it adds no access-log lines, no
404 rows, no redirect hits and no crawler rows.

The list of files is built from the sitemap, a fixed set of root files, and the
links found in every page, stylesheet, feed and sitemap that was fetched (same
site only).

## What is exported

- Every page and archive the site serves, taxonomy pages and forms pages
  linked from content.
- `sitemap.xml`, its child sitemaps and `sitemap.xsl`.
- `feed.xml`, `atom.xml`, `robots.txt`, `llms.txt`.
- The knowledge graph at `api/v1/public/knowledge-graph` (an extensionless
  file; its Content-Type comes from the live answer).
- `ads.txt` and `app-ads.txt` when they are not empty, the root icons, and the
  IndexNow key file when it is on.
- Theme files and fonts.
- Media files and variants that a fetched page, stylesheet or sitemap refers
  to.

## What is never exported

- Drafts and scheduled posts.
- Media nothing refers to, and form uploads.
- The admin address and anything under it. An export fails if a page body
  contains a private admin address.
- `/auth/`, `/plugin-ui/`, `/-/`, `/api/` (except the knowledge-graph file),
  `/preview/`, `/health`, `/ready`, `/forms/`, `/search`, `/locale`,
  `/.well-known/`, and plugin routes under `/p/` (plugin asset files are
  allowed).

## Output layout

```
index.html                    the home page
blog/my-post/index.html       an HTML page at /blog/my-post
feed.xml                      files with an extension keep their path
sitemap.xml
media/photo.jpg
api/v1/public/knowledge-graph
404.html                      the theme's not-found page
_redirects                    redirect rules for Netlify and Cloudflare Pages
old-page/index.html           a stub for a redirect at /old-page
.hatamoto-static.json         the manifest (path, sha256, size, type)
```

A redirect stub is a small page with a meta refresh, a canonical link to the
absolute target and `noindex`. A file that would collide with a directory (for
example `/a` as a file and `/a/b` as a page) fails the export.

### Redirects

Exact redirect rules are written first, sorted by source, then pattern rules in
the order the server tries them. A one-segment `*` becomes `:p1`, `:p2` and so
on, and a trailing `**` becomes `*` with `:splat`. Status codes 301, 302, 307
and 308 are kept. A rule that is invalid, has an unsafe target or cannot be
translated is skipped and reported as a warning.

Each exact rule's source is fetched, and the live answer decides: a 3xx gives a
stub and a line; a 200 means the page wins, and the rule is reported as shadowed.
Any same-site link found while crawling that answers 3xx also gets a stub and a
line.

## Directory target

```
hatamoto static export --out /srv/site-static
```

- The export is built in a hidden sibling directory (for example
  `/srv/.site-static.tmp-123`) on the same filesystem, then swapped in with two
  renames. The previous output stays as `/srv/site-static.prev`; the one before
  that is removed.
- If anything fails, the staging directory is removed and `--out` is not
  touched. If the second rename fails, the first is undone.
- The path must be absolute. These are refused: `/`, the data directory, the
  media directory, a parent of either, and an existing non-empty directory that
  has no `.hatamoto-static.json` (so a typo cannot move a real directory
  aside). An empty existing directory is fine.

Point your web server at `/srv/site-static`. Because the swap replaces the
directory, a server that holds the old directory open (some container mounts)
may need a reload.

## S3-compatible target

```
HATAMOTO_STATIC_S3_ENDPOINT=https://s3.example.com
HATAMOTO_STATIC_S3_REGION=us-east-1
HATAMOTO_STATIC_S3_BUCKET=site
HATAMOTO_STATIC_S3_PREFIX=
HATAMOTO_STATIC_S3_ACCESS_KEY=...
HATAMOTO_STATIC_S3_SECRET_KEY=...
hatamoto static export --target s3
```

When `HATAMOTO_STATIC_S3_BUCKET` is set, these variables describe the target.
Otherwise the command uses the saved hosting settings, with the secret key
opened by the server secret; if the server secret was changed, the key cannot
be opened and the command says so. The prefix is optional and scopes the export
to part of the bucket.

Objects are written with:

| Files | Content-Type | Cache-Control |
| --- | --- | --- |
| HTML, XML, feeds, text, JSON and JSON-LD, `_redirects`, the manifest | from the live answer | `public, max-age=300` |
| Fingerprinted theme files (`theme.<hash>.css`, `.js`) and fonts named by hash | from the live answer | `public, max-age=31536000, immutable` |
| Media and other assets | from the live answer | `public, max-age=86400` |

Where the live answer has no usable type, a built-in table by file extension is
used.

Order of work: upload every changed file (four at a time); if any upload fails,
stop with no deletes; delete the removed files; write the manifest object last.
Each object also carries its sha256 as metadata.

Deletes come only from the previous manifest, the list of keys an earlier
export wrote. The bucket is never listed to find things to delete, so files
that share the bucket or prefix are safe. The first export to a target has no
previous manifest: everything is uploaded, nothing is deleted, and a warning
says so. A target that overlaps the media bucket is refused.

A bucket does not turn `blog/my-post/index.html` into `/blog/my-post` by
itself. Use a host or CDN setting that serves an index document for
directories, and one that reads `_redirects` or serves the stubs.

## Known gaps

- **`?page=N` listings and knowledge-graph query URLs** (`?schema=`, `?page=`)
  are not exported. Each is skipped, counted and reported as a warning (the
  list is capped). Entities on page N stay reachable through the sitemap and
  their own addresses.
- **Computed redirects.** The legacy WordPress patterns and the bare-slug
  redirects have no rule list, so they are not written to `_redirects`. A link
  that is crawled and answers with a redirect still gets a stub and a line. The
  export warns once when the WordPress toggle is on.
- **Forms, comments, search and the tracking beacon** still post to the live
  site. A static host does not run them.
- **Response headers of `/media`** are not carried over. Set caching on the
  host (the bucket target sets Cache-Control as above).
- **Wildcard translation** follows Netlify and Cloudflare Pages. How a host
  treats an empty `*` segment can differ; the stubs cover exact rules.

## Refusals

The export stops before it writes anything, records the run as failed and
exits 1 when:

- the site access gate is on (coming soon, maintenance or password):
  `static export refused: the site access gate is on (Settings > Site access)`;
- the site settings cannot be read;
- no site address is set;
- the install is a demo;
- another export is running (`another static export is running`);
- the database has pending migrations (run `hatamoto migrate` first).

## Failures

A page that answers 5xx, a transport error, or a missing required root file
fails the whole run:

```
static: error: /blog/x: render 500
static: export failed, /srv/site-static unchanged
```

A failed export to a directory leaves the published tree as it was. S3 has no
atomic swap: if an upload fails after others succeeded, the CLI prints
`static: export failed, s3://bucket/ partially updated (no deletes)`. Nothing
was deleted and the manifest was not written; run the export again to finish.
A failure before the first upload still prints `unchanged`.

A 404 or 410 on a link inside a page is a warning, not an error: it is a broken
link on the live site. Up to 50 errors are kept.

## Run records

Each run is stored with its trigger (`cli` today), target, status, counts of
pages, assets, media, redirects, changed, unchanged, removed and warnings,
bytes, duration and errors. Only one run can be `running`. A run that stops
sending its 30-second heartbeat for five minutes is marked failed
("abandoned"), so a crash does not block the next export. Records hold no
secret, only the target (a directory, or endpoint, bucket and prefix).

Run it from the service's directory, or set `HATAMOTO_DATA` and
`MEDIA_UPLOAD_DIR`, so the server secret and media directory resolve as they do
for the running service.
