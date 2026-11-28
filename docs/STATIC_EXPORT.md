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

Owners can also switch static hosting on and run exports from Settings >
Hosting; see [Settings > Hosting](#settings--hosting) below.

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
byte, with one exception: in exported HTML, pager links to listing pages
(`?page=N`) are rewritten to the exported files (see
[What is exported](#what-is-exported)). Canonical links, `og:url` and JSON-LD
stay as the live site serves them. Nothing in the export counts as traffic: it adds no access-log lines, no
404 rows, no redirect hits and no crawler rows.

The list of files is built from the sitemap, a fixed set of root files, and the
links found in every page, stylesheet, feed and sitemap that was fetched (same
site only).

## What is exported

- Every page and archive the site serves, taxonomy pages and forms pages
  linked from content.
- Listing pages: a link to `?page=N` (N from 2 to 10000, `page` the only query
  key) on a listing is fetched and written as `<listing>/page/N/index.html`,
  for example `blog/page/2/index.html`. In exported HTML, `<a href>` values and
  `<link rel="prev|next">` values that point at such a page are rewritten to
  `/blog/page/2/`; `?page=1` points at the listing itself. The run counts them
  apart from pages (`+N listing pages`).
- `sitemap.xml`, its child sitemaps and `sitemap.xsl`.
- `feed.xml`, `atom.xml`, `robots.txt`, `llms.txt` and `llms-full.txt`. A file that is
  switched off (SEO > AI files) answers 404 on the live site, so the export skips it.
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
blog/page/2/index.html        page 2 of the listing at /blog (?page=2)
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

On Apache, including cPanel hosting, the SFTP target can also write these
rules into `.htaccess` as real 3xx answers; see
[Apache / cPanel](#apache--cpanel).

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

A private endpoint is refused: a loopback, private, link-local or
carrier-grade NAT (`100.64.0.0/10`, which includes mesh VPN addresses) address
or `localhost`. The check runs when hosting settings are saved, in the CLI (from
the environment or the saved settings) with
`endpoint refused: private or link-local address`, and again each time the
upload client connects, so a hostname that resolves to a private address
(DNS rebinding) is refused too; the Hosting form and the failed run both name
the setting below. To use a storage service on a private network, such as a LAN
MinIO or one reached over a mesh VPN, set `HATAMOTO_STATIC_S3_ALLOW_PRIVATE=1`
in the environment of the server and the CLI. It turns the address check off
for every range, loopback and link-local included. Without it the upload client
does not use `HTTPS_PROXY`; with it, it does.

Objects are written with:

| Files | Content-Type | Cache-Control |
| --- | --- | --- |
| HTML, XML, feeds, text, JSON and JSON-LD, `_redirects`, the manifest | from the live answer | `public, max-age=300` |
| Fingerprinted theme files (`theme.<hash>.css`, `.js`) and fonts named by hash | from the live answer | `public, max-age=31536000, immutable` |
| Media and other assets | from the live answer | `public, max-age=86400` |
| Media whose type is outside the upload allowlist | `application/octet-stream` | `public, max-age=86400`, plus `Content-Disposition: attachment` |

Where the live answer has no usable type, a built-in table by file extension is
used.

Objects written by an earlier version lack the attachment header: run one full
export (Rebuild all in Settings > Hosting) after upgrading so they pick it up.

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

## SFTP target

For shared hosting (cPanel and similar) that gives you an SSH login but not a
web-server config. Choose "SFTP (cPanel / shared hosting)" in Settings >
Hosting. It is set up on that screen and run by the export worker; the
`hatamoto static` command keeps `--target dir|s3`.

Fields:

- **Host** and **port** (22 by default), and **user**.
- **Auth:** a password, or an OpenSSH private key. A key with a passphrase is
  refused: use one without. The password or key is stored sealed, never shown
  again, and a blank field keeps the saved one.
- **Remote folder**, relative to the login home, `public_html` by default. These
  are refused: an empty value, `/`, an absolute path, `~`, `.` and anything with
  a `..` segment.
- **Write .htaccess** (on by default); see [Apache / cPanel](#apache--cpanel).

```
host: ssh.example.com   port: 22   user: site
remote folder: public_html
```

**Trust host key.** Hatamoto never accepts a host key silently. After you save
the host, press *Trust host key* on the Hosting page. It connects to the host
(handshake only, no password or key is sent) and shows the SHA256 fingerprint.
Compare it with the fingerprint your provider shows (cPanel lists it under SSH
access) and confirm with your password. Only that action stores the
fingerprint; an export or Test export never does. When the host presents a
different key later, the export is refused with a message that names Trust host
key: check why it changed, then trust it again. Changing the host or the port
clears the trusted key, and switching to static mode with this target needs one.

The credential is sealed with a key context of its own, derived from the server
secret. It is never opened as an S3 key, and the S3 secret is never used as an
SFTP password. If `HATAMOTO_SECRET` was changed, the saved credential cannot be
opened: enter it again.

A host on a private network (loopback, private, link-local or carrier-grade NAT
ranges, or `localhost`) is refused when settings are saved and again each time
the export connects, so a name that resolves to a private address is refused
too. To publish to a host on your own network, set
`HATAMOTO_STATIC_SFTP_ALLOW_PRIVATE=1` in the environment of the server. It is
separate from `HATAMOTO_STATIC_S3_ALLOW_PRIVATE`: allowing a private S3 endpoint
does not open SFTP.

Order of work: each changed file is uploaded to a temporary file
(`.<name>.hatamoto-tmp`) beside its place and then renamed over it (an atomic
rename where the server offers one, else the old file is removed first); if any
upload fails, stop with no deletes; delete the removed files; update the
`.htaccess` block; write the manifest last. The export connects once to read
the previous manifest and once to publish, so no connection is held while the
site is built.

Deletes come only from the previous manifest, the list of files an earlier
export published. The folder is never listed to find things to delete. The first
export into a folder has no manifest: everything is uploaded, nothing is
deleted, and a warning says so. Other files already in the folder are left
alone, but a file with the same name is overwritten. Use an empty folder, or one
only Hatamoto publishes. If the folder holds an `index.php` (a default page or
another CMS), the host's `DirectoryIndex` may serve it before `index.html`;
remove it or the site shows the wrong page.

## Hosting setups

There are four ways to publish the static files. Settings > Hosting shows a
"Set up your web server" card for the saved settings, with the exact
configuration for your folder; the examples below show its shape.

### Same host (default)

One host serves everything. Your web server serves the export folder and passes
the paths only Hatamoto answers to Hatamoto. There is no live origin, so there
is no CORS and no second hostname: pages, forms, search and comments all use the
site address. In the hosting settings this is the *Same host* choice, which
saves an empty live origin.

The web server passes these paths to Hatamoto:

- `/forms/`, `/-/` (for example the comment form), `/f/`, `/p/`, `/api/`,
  `/auth/`, `/plugin-ui/` and `/preview/`, and `/mcp`;
- `/search` and `/<locale>/search`;
- the admin path (`/admin` here, or the path you configured);
- the public API prefix, when it is set and not already under `/api/`;
- `/theme/assets/hm-live.js`, the script that loads the comment form.

`/media/`, `/theme/`, `/fonts/` and `/.well-known/` are served from the export
folder when the file is there and passed to Hatamoto when it is not, so an
upload newer than the last export still works and a certificate client can
write its challenge files into the folder. A theme preview (the
`hm_theme_preview` query or cookie) is always passed to Hatamoto.

Everything else is a file from the export folder. A missing file answers with
`/404.html`. `/_redirects` and `/.hatamoto-static.json` are answered with 404.

The upstream address is the one Hatamoto listens on (`127.0.0.1:8090` here). A
listen address of `0.0.0.0`, `::` or none is shown as `127.0.0.1`; change it in
the config if your web server reaches Hatamoto differently. Loopback is a
trusted proxy by default, so the forwarded address and scheme headers work
without further setup.

#### Caddy

```
example.com {
	@hatamoto {
		path /admin /admin/* /api/* /auth/* /f/* /forms/* /mcp /p/* /plugin-ui/* /preview/* /search /-/* /theme/assets/hm-live.js
	}
	@hatamoto_search path_regexp ^/[^/]+/search$
	handle @hatamoto {
		reverse_proxy 127.0.0.1:8090
	}
	handle @hatamoto_search {
		reverse_proxy 127.0.0.1:8090
	}
	handle {
		root * /srv/site/public
		@hidden path /_redirects /.hatamoto-static.json
		respond @hidden 404
		header {
			X-Content-Type-Options "nosniff"
			X-Frame-Options "SAMEORIGIN"
			Referrer-Policy "strict-origin-when-cross-origin"
		}
		redir /old-page /new-page 301
		try_files {path} {path}/index.html
		file_server
	}
	handle_errors {
		root * /srv/site/public
		rewrite * /404.html
		file_server
	}
}
```

#### nginx

```
server {
	listen 80;
	# Add your TLS lines here: listen 443 ssl; ssl_certificate ...;
	server_name example.com;
	root /srv/site/public;

	location ^~ /forms/ {
		proxy_pass http://127.0.0.1:8090;
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
		proxy_set_header X-Forwarded-Proto $scheme;
	}
	# ... the same block for /-/, /f/, /p/, /api/, /auth/, /plugin-ui/,
	# /preview/, the admin path, and exact locations for /mcp, /search
	# and /theme/assets/hm-live.js
	location ~ ^/[^/]+/search$ {
		proxy_pass http://127.0.0.1:8090;
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
		proxy_set_header X-Forwarded-Proto $scheme;
	}

	location = /_redirects { return 404; }
	location = /.hatamoto-static.json { return 404; }
	location = /old-page { return 301 /new-page; }

	location / {
		add_header X-Content-Type-Options "nosniff" always;
		add_header X-Frame-Options "SAMEORIGIN" always;
		add_header Referrer-Policy "strict-origin-when-cross-origin" always;
		try_files $uri $uri/index.html =404;
	}
	error_page 404 /404.html;
}
```

The generated config is longer: it lists every path, adds the full header set
and translates your redirects (see [Redirects](#redirects-on-a-plain-web-server)
and [Security headers](#security-headers)). Security headers are set only on
the files your web server serves. Hatamoto sets its own on what it answers, and
the admin has its own policy.

### Separate live origin

The files are served from the site address and the islands answer on a second
hostname, the live origin. Use it when the site address cannot reach Hatamoto,
for example a bucket behind a CDN. Choose it under *Advanced: separate live
origin* in the hosting settings.

- The live origin needs its own DNS name and a proxy to Hatamoto. It must differ
  from the site address in its host.
- The admin moves to the live origin.
- The live origin answers CORS for the site address only, and redirects page
  GETs to the static site.
- The Content Security Policy of the static site must allow the live origin in
  `script-src`, `connect-src` and `form-action`. The generated config does this.

The generated Caddy and nginx configs then have two sites: one serving the
files without a proxy, and one for the live host that proxies everything to
Hatamoto (`reverse_proxy 127.0.0.1:8090` in Caddy, `proxy_pass` in nginx).

### S3 + CDN

For an S3-compatible bucket the card shows guidance instead of a web server
config, because a bucket cannot pass requests to Hatamoto.

- **Headers.** Add the security headers at the CDN (see
  [Security headers](#security-headers)): `X-Content-Type-Options`,
  `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`, and the
  Content-Security-Policy. Cloudflare uses Transform Rules (response header
  modification); CloudFront uses a response headers policy.
- **Redirects.** Netlify and Cloudflare Pages read `_redirects`. A plain bucket
  does not: exact rules fall back to the meta-refresh pages the export writes,
  and wildcard rules are lost. See
  [Redirects](#redirects-on-a-plain-web-server).
- **Islands.** Use a separate live origin, unless the CDN routes the island
  paths listed under [Same host](#same-host-default) to Hatamoto.
- **Admin.** With a separate live origin the admin is on the live host. If the
  CDN routes the paths to Hatamoto, it stays on the site address.
- **Directories.** Serve an index document for directories, as described under
  [S3-compatible target](#s3-compatible-target).

### Security headers

Static pages should get what public pages get from Hatamoto: `X-Frame-Options:
SAMEORIGIN`, `X-Content-Type-Options: nosniff`, `Referrer-Policy:
strict-origin-when-cross-origin`, `X-XSS-Protection: 0`, a `Permissions-Policy`
and the page Content-Security-Policy. The policy follows the site's
mode (enforcing by default; report-only on older installs until switched, or
whatever `CSP_ENFORCE` says). An export writes no headers; they come from the
web server or CDN config the setup card shows, so copy that config again after
switching the mode. `Strict-Transport-Security` is added only in production.
The setup card lists the header names it includes.

### Apache / cPanel

Apache reads no `_redirects`, so with the SFTP target the export keeps a block
in the `.htaccess` file of the remote folder. The block sits between
`# BEGIN Hatamoto` and `# END Hatamoto`. Each export reads the remote file,
replaces only that block (or appends it when there is none) and writes the file
through a temporary file and a rename. Every other line is kept as it was. When
there are no redirect rules the block is removed. The file is not part of the
manifest, so an export never deletes it.

```
# BEGIN Hatamoto
<IfModule mod_alias.c>
RedirectMatch 301 ^/old/?$ /new
</IfModule>
<IfModule mod_rewrite.c>
RewriteEngine On
RewriteRule ^a/([^/]+)/?$ /b/$1 [R=301,L]
</IfModule>
# END Hatamoto
```

- An exact rule is `RedirectMatch <status> ^/from/?$ /to`. Plain `Redirect` is
  not used: it matches by prefix, so a rule for `/blog` would also redirect
  `/blog/post-1`. A source or target with whitespace is skipped and reported as
  a warning.
- A wildcard rule is `RewriteRule ... [R=<status>,L]`. `RewriteEngine On` and
  mod_rewrite are needed only when wildcard rules exist.
- The rules sit inside `<IfModule>`, so a missing module skips them instead of
  failing the site. The block has no access-control directives.
- Turning off **Write .htaccess** removes nothing at once. The next export
  removes the block.
- If the site shows a 500 after an export, the host forbids these directives
  (`AllowOverride` does not allow them). Turn **Write .htaccess** off and export
  again.
- `_redirects` and the meta-refresh stubs are still written, so crawlers and
  browsers that skip the server rules still follow redirects.
- `/.hatamoto-static.json` and `/_redirects` can be read on Apache: they are
  public file lists, with no secrets.

The Apache / cPanel tab of the setup card shows a preview of the block built
from your redirect rules. The export also writes redirects it finds while
crawling, so the block in the file is the authoritative one.

### Redirects on a plain web server

`_redirects` is read by Netlify and Cloudflare Pages, not by Caddy, nginx or a
bucket. The setup card counts the exact and the wildcard rules in the last
export's `_redirects` (for a directory target) and translates them:

- Caddy: an exact rule becomes `redir <from> <to> <status>`; a wildcard rule
  becomes a `path_regexp` matcher and a `redir` that uses the captures.
- nginx: an exact rule becomes `location = <from> { return <status> <to>; }`; a
  wildcard rule becomes a regular-expression `location` that returns with the
  captures.

Rules the generator cannot express safely (a status other than 301, 302, 307 or
308, unusual characters, a placeholder inside text) are left out as a
`# skipped:` comment and listed on the card. For an S3 target the card shows
only the total from the last run. On Apache, the SFTP target writes them to
`.htaccess` itself; see [Apache / cPanel](#apache--cpanel).

### Checklist

- The parent of the export folder is writable by the service user (the card
  checks it).
- The security headers are set where the files are served.
- `/_redirects` and `/.hatamoto-static.json` are not served.
- The site address answers; the card tests it after an export and shows "Not
  checked yet" until then.

## Known gaps

- **Date filters are frozen at export time.** A query loop filtered relative to
  today shows the list as of the export; export again to refresh.
- **Other query URLs**, such as the knowledge graph's `?schema=` and a
  `?page=` combined with another key, are not exported. Each is skipped,
  counted and reported as a warning (the list is capped). Listing pages
  (`?page=N` alone) are exported.
- **Listing page collisions.** If the file of a listing page is taken by a real
  page, the real page wins and the export warns that the listing page is
  replaced or not exported. A listing page that redirects is not exported and
  is reported as a warning.
- **Computed redirects.** The legacy WordPress patterns and the bare-slug
  redirects have no rule list, so they are not written to `_redirects`. A link
  that is crawled and answers with a redirect still gets a stub and a line. The
  export warns once when the WordPress toggle is on.
- **Forms, comments, search and the tracking beacon** still post to the live
  site. A static host does not run them.
  In static mode they stay on the site address when the web server passes their
  paths to Hatamoto (see [Same host](#same-host-default)), or, with a live
  origin saved in the hosting settings (`live_origin`), exported pages send
  them to that live origin, which answers CORS for the site address only and
  redirects its page GETs to the static site; see
  [THEMES.md](THEMES.md#static-mode). The live origin answers the preflight of
  `hx-post` islands for these request headers: `Content-Type`, `X-Page-URL`,
  `HX-Request`, `HX-Current-URL`, `HX-Target`, `HX-Trigger`, `HX-Trigger-Name`,
  `HX-Boosted` and `HX-Prompt`. The new-comment post also answers the static
  site's CORS preflight (that origin only, no credentials, and only in static
  mode); comment edit and delete do not.
  Erasing a commenter anonymises their comments and re-exports the site
  (trigger `comment`).
- **Response headers of `/media`** are not carried over, except the types and
  the attachment disposition above. Set caching on the host (the bucket target
  sets Cache-Control as above). Object metadata
  cannot hold the rest, so set these as CDN response-header rules for `/media/`,
  as the live server does: `X-Content-Type-Options: nosniff` on everything;
  `Content-Security-Policy: sandbox` on SVG and on any type that is not an
  allowed media type, but not on PDF files (it breaks the browser's PDF
  viewer); and for PDF files `Content-Security-Policy: frame-ancestors 'self'`
  with `X-Frame-Options: SAMEORIGIN`.
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
- the S3 endpoint is a private or link-local address
  (`endpoint refused: private or link-local address`; see
  [S3-compatible target](#s3-compatible-target));
- the SFTP host key is not trusted yet, or the host now presents a different one
  (`Trust host key` on the Hosting page);
- the SFTP host is a private address (see [SFTP target](#sftp-target));
- the SFTP private key is encrypted (use one without a passphrase);
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
`static: export failed, s3://bucket/ partially updated (no deletes)`. An upload
failure deletes nothing and writes no manifest. If a delete or the manifest
write fails after objects were deleted, the line is `partially updated`. Run
the export again to finish. A failure before the first upload or delete still
prints `unchanged`.

A 404 or 410 on a link inside a page is a warning, not an error: it is a broken
link on the live site. Up to 50 errors are kept.

## Settings > Hosting

Owners find the page under Settings > System > Hosting. It holds the mode, the
target, the last export, the export log and the list of live islands.

**Modes.** *Served* (the default) answers every page from Hatamoto. *Static
files + live islands* publishes the pages as files and keeps the interactive
parts (forms, search, comments, consent, the pageview beacon and plugin public
routes) on this server, which answers them for the static site. The page lists
the live islands and the ones that stay on Hatamoto, read from the island
inventory. On a served site the list shows which islands would stay live once
the site is static, with no status until then. Switching the mode asks for your password again. It is refused while
Site access or the demo is on, and while the target is incomplete.

**Target.** A local directory, S3-compatible storage (service, endpoint,
region, bucket, folder prefix, access key and secret key), or SFTP (host, port,
user, password or key, remote folder, Trust host key and the Write .htaccess
switch; see [SFTP target](#sftp-target)). The page shows only
the fields of the chosen target; the fields of the other one keep their saved
values. The secret key is
stored sealed with a key derived from the server secret, is never shown again
and a blank field keeps the saved one. The live islands card offers *Same host*
(the default: your web server passes the island paths to Hatamoto, and the live
origin stays empty) or, under *Advanced: separate live origin*, an address where
the islands answer; that one must differ from the site address in its host.
The *Set up your web server* card shows the config for the saved settings (see
[Hosting setups](#hosting-setups)); for SFTP it has an Apache / cPanel tab.
Rolling back to an older release while an SFTP target is saved makes exports
fail with an unknown target: switch the target back to a folder or S3 first. While the site is static,
a Save that changes the target, any of its fields, the secret key or the live
origin, including switching to Same host, asks for your password. Nothing is kept across the password page, so
enter the changes again.

**What starts an export.** With the worker running and the mode set to static,
an export starts about 30 seconds after the last of these changes (at most
about 2.5 minutes under constant edits):

- an entity is published, unpublished, scheduled or changed;
- menus, media, redirects or an approved comment change;
- the theme or the site settings change, which re-publishes every file.

On a served site, or without the worker, nothing runs by itself. Only one
export runs at a time: if another process holds the run, the export is
retried after the next window.

**Buttons.** All three are always shown. *Test export* is for a served
site: it writes the site to the saved target, with islands rewritten as in
static mode, without changing what visitors get. In static mode it is disabled
("In static mode, use Export now.") because it would publish to the live
target; use *Export now* there. It needs a saved target ("Save a target
first."), does not ask for your password, sends no failure mail, shows no
failure banner and appears in the log as "Test export". With no live origin
(Same host), a test export points islands at the site address, as static mode
does; without a Site URL they keep this site's relative paths. A test export writes real files:
a directory your web server already serves is published. *Export now* starts an
export without waiting. *Rebuild all* re-publishes every file instead of only
the changed ones (removed files are still deleted) and asks for your password.
Both are disabled with the reason when the site is served ("Needs static mode.
Use Test export first.") or the worker is off ("The export worker is off on
this server."). Only one export runs at a time, test exports included.

**Last export.** After a failed run the line reads "Failed <time ago> ·
<first error>" and, when there is one, "last good:" with that run's pages, size,
duration and errors. Test exports are not counted.

**Failures.** A failed run shows a banner on the dashboard and on the Hosting
page, and every owner gets one email when a run fails after a run that did not.
Further failures in the same streak do not send more mail.

**Backup restore.** A backup restore switches the restored site to served, as
it switches off-site backups off: check the target, then switch static on
again. A restored copy never exports into the original's target.

AI crawler analytics read the access log, and a static site does not pass
through this server. See [access log ingest](site/ai-seo/access-log-ingest.md)
to count those visits.

## Run records

Each run is stored with its trigger (`cli`, `manual`, `rebuild`, `mode` for the
first export after switching to static, or the reason of a change: `entity`,
`change`, `redirects`, `comment`, `theme`, `settings`), target, status, counts of
pages, assets, media, redirects, changed, unchanged, removed and warnings,
bytes, duration and errors. Only one run can be `running`. A run that stops
sending its 30-second heartbeat for five minutes is marked failed
("abandoned"), so a crash does not block the next export. Records hold no
secret, only the target (a directory, or endpoint, bucket and prefix). Runs
older than 90 days are deleted after each export; a running run never is.

Run it from the service's directory, or set `HATAMOTO_DATA` and
`MEDIA_UPLOAD_DIR`, so the server secret and media directory resolve as they do
for the running service.
