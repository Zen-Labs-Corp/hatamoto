# Command line

`hatamoto` runs operator commands directly against `DATABASE_URL`, over the same services the API and the MCP server use. Output goes to stdout; logs (warnings and errors) go to stderr.

```
hatamoto entity list [--schema S] [--status S] [--search Q] [--limit N]
hatamoto entity get <id>
hatamoto entity create --schema S --name N [--slug SLUG] [--attributes JSON | --attributes-file F] [--publish] [--as EMAIL]
hatamoto entity publish <id> [--as EMAIL]
hatamoto entity unpublish <id> [--as EMAIL]

hatamoto redirect list [--search Q]
hatamoto redirect create --from /old --to /new [--status 301]
hatamoto redirect delete <id>

hatamoto analytics crawlers [--from D] [--to D] [--limit N]      # AI-crawler fetches per page and agent
hatamoto analytics first-crawls [--from D] [--to D] [--limit N]
hatamoto analytics referrals [--from D] [--to D]                 # AI-assistant visits per day

hatamoto export <file.tar.gz> [--no-submissions]
hatamoto import <file.tar.gz>
hatamoto import wordpress (--url URL --user U --app-password P | --wxr FILE) [--scan-only] [--resume RUN] [--audit-url URL]

hatamoto builder convert [--dry-run] [--verify-render] [--entity ID]

hatamoto keys generate [--name N] [--dir D]
hatamoto theme pack <dir> [--key F] [--out F]
hatamoto theme pack (--archive F --sha256 H | --package F) [--registry] [--expect-name N] [--screenshot-out D] [--key F] [--out F]
hatamoto theme check <dir> | --archive F --sha256 H | --package F [--registry] [--expect-name N] [--screenshot-out D]
hatamoto plugin pack <dir> [--key F] [--out F]
hatamoto theme dev <dir> [--no-activate]
hatamoto package install <file|url> [--grant P,P|all]
hatamoto package list [--json]
hatamoto theme activate <name|default> [--force]
hatamoto apikey create --name N --scopes a,b [--out FILE] [--as EMAIL] [--description D] [--rate-limit N]
hatamoto admin-url                                                # print the admin address (the full link when PUBLIC_BASE_URL or the site address is set)
hatamoto admin-url set <path> [--i-understand-weak]               # store a new admin address; a weak one is refused without the flag
hatamoto admin-url random                                         # store a new random admin address
hatamoto plugin settings set <plugin> key=value...   # key=@FILE or key=- reads the value from a file (max 1 MiB) or stdin; `@@x` sets the literal `@x`
hatamoto supervise                                                # run `serve` as a child; releases, health gate, rollback
hatamoto update apply --file F --record R [--sig S]               # install a signed release from local files
hatamoto update rollback [--force]                                # back to the previous release (--force: even a bad-listed one)
hatamoto update status                                            # mode, channel, pin and the last update check (needs the database)
hatamoto update check [--dry-run]                                 # check the channel now (--dry-run: decide, install nothing)
hatamoto update pin vX.Y | unpin                                  # stay on one minor line; security releases still apply
hatamoto update apply vX.Y.Z [--force] [--no-backup]              # download, verify and install a named release, majors too; --no-backup skips the pre-update backup
hatamoto backup create [--config P]                               # full backup to $HATAMOTO_DATA/backups (needs the database); --config stores your env file
hatamoto backup list [--json]                                     # needs only the data dir
hatamoto backup delete <id>
hatamoto backup restore <id|path.tar> [--dry-run] --admin-url DSN [--config-out P]   # rebuild a clean machine from a backup; --dry-run checks and writes nothing
hatamoto registry index <dir> --key F [--meta F] [--out F] [--url-prefix P] [--valid-days N]   # N is 1 to 180, default 90
hatamoto registry page <registry.json> [--out F] [--title T] [--intro T] [--write-url U] [--submit-url U] [--install-hint T] [--trusted-key K]   # static plugin directory page from a signed index, for operators without a CMS page (the Plugin directory section needs no CLI)
```

`supervise`, `update apply` and `update rollback` need no database. `supervise` is what the systemd unit and the image run; `update apply` verifies the record signature and the artifact hash against the keys built into the binary, stages the release under `$HATAMOTO_DATA/releases/<version>` and signals the supervisor to switch. A supervisor counts as running while it holds the lock on `supervisor.pid`. Without one, the command moves the links and tells you to restart; that release is health-gated at the next supervisor start. Run it as the service user (never root) and from the service's directory or with `HATAMOTO_DATA` set: the data dir is `./data` otherwise, and the command refuses a directory with no supervisor state. `update rollback` refuses a bad-listed previous release unless `--force`. Those two commands fetch nothing from the network. `update status|check|pin|unpin` read the update settings and need `DATABASE_URL`; `update apply vX.Y.Z` downloads from the release channel. `update apply vX.Y.Z` takes a backup first and exits non-zero, installing nothing, when it fails; `--no-backup` skips it for that run. `backup create|list|delete|restore` write and read `$HATAMOTO_DATA/backups`; run them as the service user. `backup restore` needs `--admin-url`, a superuser connection string used once for the plugin role setup, and `DATABASE_URL` set to the app role on an empty database; `--dry-run` runs every check and writes nothing. `backup create` and the backup that `update` takes read media from `MEDIA_UPLOAD_DIR` (default `./uploads`, relative to the current directory), so run them from the service's directory or set `MEDIA_UPLOAD_DIR`; a missing media directory or missing referenced files fail the backup. See [BACKUPS.md](BACKUPS.md) and [UPGRADES.md](UPGRADES.md).

`admin-url` needs `DATABASE_URL`. A new address is stored at once and applies after a restart; the command says so. When `HATAMOTO_ADMIN_PATH` is set, that pin is what the server serves and what `admin-url` prints, and the command adds a note with the stored value. A path that is invalid or reserved (for example one a page or redirect already uses) is refused and the command exits 1. A weak address (easy to guess) is refused unless you pass `--i-understand-weak`, which records that you accepted it. The address is private to your install: it is not a stable contract.

`hatamoto healthcheck` exits 0 when the local server answers `/health` (the image's `HEALTHCHECK`; uses `API_PORT`).

Unattended installs (no admin session needed; each writes the database and a running server applies it within seconds):

- `theme activate` runs the checks of Admin > Themes (the theme must exist, render, and cover the section types the content uses; `--force` skips the last) and is audited. `default` is the built-in theme. The CLI runs no plugin code, but it knows the template funcs, partials and sections of the installed, enabled plugins (a func renders empty during the check), so install the plugins first and the theme that calls them second. Unlike the admin, which activates after showing the missing sections, the CLI refuses to activate while sections are missing unless you pass `--force`.
- `apikey create` prints the key **once**: to the new file `FILE` with mode 0600 (an existing file is never overwritten, and no key is created if the file cannot be written), or, without `--out`, alone on stdout (the details go to stderr). Scopes are validated as in the admin. A key acts with its creator's authority, so it is created as `--as EMAIL` or the oldest owner. The audit log records name, scopes and prefix, never the key.
- `plugin settings set` takes top-level `key=value` pairs. Values are read as the property's schema type (strings stay text; numbers, booleans, arrays and objects are JSON), merged over the stored settings (write-only values such as secrets are kept unless set), validated against the plugin's `settings_schema` as in the admin, saved and audited (key names only). Put a secret in a file or on stdin, not on the command line, where other local users can read it: `plugin settings set acme token=@/run/secrets/acme` or `... token=-`. The result is validated with the schema defaults applied, so a required property that has a default need not be set. Cached sitemaps that depend on a setting refresh with their TTL.
- `theme check` validates a theme (theme.json, screenshot, package limits) from a directory, an author zip (`--archive F --sha256 H`) or a packed `.hmp` (`--package F`) without a key, and writes nothing except the screenshot when `--screenshot-out D` is given. `--registry` adds the rules of the theme directory: a valid screenshot is required and `default` is refused; `--expect-name N` refuses a theme with another name. `theme pack` takes the same source flags and options, for registry builders. See [THEMES_WRITE_AND_SUBMIT.md](THEMES_WRITE_AND_SUBMIT.md).
- `package list` shows installed plugins and themes with version, source, signer and the active theme.

Packages (signing, trust, the registry): see [PACKAGES.md](PACKAGES.md); to submit a plugin, see [PLUGINS_WRITE_AND_SUBMIT.md](PLUGINS_WRITE_AND_SUBMIT.md); to submit a theme, see [THEMES_WRITE_AND_SUBMIT.md](THEMES_WRITE_AND_SUBMIT.md). `package install` and `theme dev` write the database; a running server applies the change within seconds.

Dates are `YYYY-MM-DD` (UTC); the default range is the last 30 days.

## builder convert

`hatamoto builder convert` moves pages from `attributes.page_sections` to a `page_doc` v2 tree ([BUILDER.md](BUILDER.md)).

```
hatamoto builder convert --dry-run --verify-render     # report only; writes nothing
hatamoto builder convert                               # convert every page
hatamoto builder convert --entity <id>                 # one page
```

- Each page is rendered both ways with the same view and the bytes are compared **before** any write. Real runs always verify; `--verify-render` makes a dry run do it too. A page that renders differently is not written, and the command exits 1.
- Writes use the entity service (`UpdateEntity`, summary "Converted to builder doc"): one version per page, the stored `page_sections` left as it is. The page keeps its modified date (`updated_at`): a lossless shape change is not a content edit, so `dateModified` and the sitemap `lastmod` do not change. Pages that already have a `page_doc` are skipped, so a second run changes nothing. `--revert` is no longer supported: it exits 1 with "unsupported: the page_sections mirror is no longer maintained".
- `serve` logs `pages without page_doc` once at boot with the number of pages still to convert (a read-only count).
- Output: one row per page (`id`, `slug`, result), then `pages: N convertible: N render-identical: N diffs: N skipped (already v2): N`.
- Verification uses the built-in theme; the CLI does not load the active theme package. Like the other CLI writes it does not fire plugin events or run plugin `before_save`; a running server clears its page cache within seconds after the change.

## Notes

- `entity create` makes a draft unless `--publish` is given. Attributes are validated against the schema and rich-text HTML is sanitised, as in the API. The theme-driven sanitising of HTML section fields needs the running server's theme; rendering still sanitises them.
- The CLI is a separate process, but its page and menu writes add a row to a content change log in the database. A running server notices it within about one poll interval (3 seconds, 1 outside production) and clears its page cache, so a published page shows without a restart. While writes keep arriving (an import) purges are coalesced and a change can take up to about 15 seconds to show, followed by a final purge once the writes stop.
- `--as EMAIL` attributes the change (`created_by` / `updated_by`) to an existing user; without it the columns stay empty.
- The CLI builds the entity service without the server's plugin host, so it does **not** fire `entity.*` plugin events (so webhooks send nothing for CLI changes), does not run plugin `before_save` validation, and does not reindex internal links or rewrite links on a slug change. Use the API or MCP server when those must run; the CLI is for operator and recovery work. Redirect commands bypass nothing of the redirect rules (same validation); a running server reads redirect rules again within about a minute.
- `user create` and `user reset-password` are documented in the README; they also work on an imported database.
- Export and import: see [EXPORT.md](EXPORT.md). Importing a WordPress site: see [IMPORT-WORDPRESS.md](IMPORT-WORDPRESS.md).
