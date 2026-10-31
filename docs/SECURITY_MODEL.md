# Security model

This page says what Hatamoto protects, from whom, and which test fails when a
protection breaks. To report a problem, read [`SECURITY.md`](../SECURITY.md).
The keys behind updates and signed packages are listed, with fingerprints, in
[Signing keys and fingerprints](site/updates/keys.md).

Every row of the tables below names a Go package (under `backend/`) and the
tests that enforce the guarantee. A test name that does not exist is a bug in
this page; the proof that they all exist is a `go test -list` run per package
(see "Check this page" at the end). Rows marked "Postgres" need a database and
run with `-tags=integration`; the others run with plain `go test`.

## Who is trusted

| Party | Trusted to | Not trusted to |
|---|---|---|
| The server's owner and admins | Do anything the admin allows, install code, change settings | Nothing: an owner can run any code on their own server by design |
| Editors and API keys | Do what their role or key scopes allow | Reach owner-only screens, or act outside a key's scopes |
| Themes | Everything the template engine can do: a theme is trusted code like the binary | Be installed from an unknown signer (refused until the owner trusts the key) |
| Plugins | Only the capabilities the owner granted, inside a WASM sandbox | Anything else: see "Plugins" |
| The update channel and registry hosts | Nothing on their own: every release is checked against keys compiled into the binary, and every package against the first-party key plus the keys in `HATAMOTO_TRUSTED_KEYS` | Shipping code that is not signed |
| Visitors | Read public pages, submit public forms | Everything else |
| Reverse proxies | Tell the server the client address, only when listed in `HATAMOTO_TRUSTED_PROXIES` (loopback by default) | Anything from an unlisted address |

## Guarantees and the tests that enforce them

### Plugin sandbox

A plugin is a WebAssembly module run by the host. The module has no network, no
file system and no host access except through capabilities the owner granted.

| Guarantee | Package | Tests |
|---|---|---|
| A guest cannot open a connection, even when its manifest lists a host | `internal/wasmhost` | `TestNoNetwork` |
| A guest cannot read a host file | `internal/wasmhost` | `TestNoFilesystem` |
| Memory is capped, also under concurrent attempts | `internal/wasmhost` | `TestMemoryBombHitsCap`, `TestMemoryCapRejectsTinyModuleBudget`, `TestMemoryCeilingIsTwiceMaxPages`, `TestConcurrentMemoryBombsBounded` |
| A call that runs too long is stopped, with or without a caller deadline | `internal/wasmhost` | `TestTimeoutIsEnforcedWithoutCallerDeadline`, `TestPerHookTimeouts`, `TestSlotWaitIsBoundedWithoutCallerDeadline` |
| Guest log output is truncated and rate limited | `internal/wasmhost` | `TestGuestLogsAreTruncatedAndRateLimited` |
| A capability is denied until the owner grants it, and a grant the manifest does not request is refused | `internal/pluginhost` | `TestDeniedUntilGranted`, `TestGrantMustBeRequested` |
| A host call request over 1 MiB is refused | `internal/wasmhost` | `TestHostCall` |
| Guest logging, `mail.send` and `http.fetch` are rate limited per plugin; other host calls are not rate limited | `internal/pluginhost` | `TestHostCallLimits` (log), `TestFetchRateCapPerPlugin` (`http.fetch`), `TestMailSendRateLimitPerPlugin` (`mail.send`: per recipient, per plugin) |
| A plugin's admin page runs in a sandboxed iframe and its bridge carries only granted capabilities | `internal/admin` | `TestPluginUIPageIsASandboxedIframe`, `TestBridgeCarriesOnlyGrantedCapabilities`, `TestBridgeIsRateLimitedPerUserAndPlugin` |

### Plugin database role

Each plugin that asks for `db.*` gets its own Postgres login role that owns one
schema and nothing else. The SQL guard in the host is a second layer: the tests
below switch it off and attack the database itself.

| Guarantee | Package | Tests |
|---|---|---|
| The role has no privilege on core tables, and one plugin's role cannot reach another plugin's schema | `internal/pluginhost` (Postgres) | `TestPluginRoleHasNoCorePrivileges`, `TestPluginsAreIsolatedFromEachOther` |
| Escape attempts hold even without the SQL guard | `internal/pluginhost` (Postgres) | `TestPluginEscapesHoldWithoutTheGuard`, `TestEscapePayloadsThroughTheGuard` |
| A database dump uses the plugin's role and keeps the database password out of the command line | `internal/backup` | `TestDumpPluginUsesRoleAndKeepsPasswordOutOfArgs` |

### Outbound requests from plugins (`http.fetch`)

| Guarantee | Package | Tests |
|---|---|---|
| Private, loopback and link-local targets are refused | `internal/pluginhost` | `TestBlockedAddr`, `TestFetchBlocksPrivateTargets` |
| Only hosts the manifest lists are reachable; a wildcard needs the `http.fetch.any` grant | `internal/pluginhost` | `TestFetchAllowlistAndURLRules`, `TestHostAllowed`, `TestWildcardHostNeedsTheFetchAnyGrant` |
| A DNS answer is resolved once, so rebinding cannot swap the address | `internal/pluginhost` | `TestFetchRebindingResolvesOnce` |
| Every hop, including a redirect, is dialled through the address guard; redirects off the allowlist and redirect loops are refused | `internal/pluginhost` | `TestFetchBlocksPrivateTargets`, `TestFetchRedirects` |
| Size and time caps apply, and proxy environment variables are ignored | `internal/pluginhost` | `TestFetchCaps`, `TestFetchIgnoresProxyEnvironment` |
| Widening a plugin's hosts or adding `http.fetch.any` in an update needs the owner's confirmation | `internal/packages` (Postgres) | `TestWidenedHostsOrFetchAnyNeedConfirmationAndResetGrants` |

If the operator sets `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1`, objects the
application role does not own and that are open to PUBLIC are readable by every
plugin role; by default the host refuses `db.*` while any exist.

The same address guard protects other outbound features: outgoing webhooks
(`TestPrivateAddressRefusedAtDialTime` in `internal/webhook`) and the WordPress
importer (`TestClientIsSSRFGuarded` in `internal/wpimport`).

### Browser JavaScript and markup from plugins (`site.scripts`)

A plugin's script on public pages, and any markup of its sections and partials
that could run script, needs the owner's `site.scripts` grant. Without it the
script is not linked and the template is not registered. Plugin templates get
the same markup check as widget packs (see `docs/PLUGIN_API.md` for the list),
with two exceptions: a `<form>` whose `action` is an http(s) or relative URL,
and CSS: `<style>` elements and `style` attributes. Without `site.scripts` a
plugin's sections can therefore still restyle the whole page.

Without the grant a structural rule applies as well. Each branch of a template
(the body and every `if`/`else`/`range`/`with` body) has every action replaced
by an inert token and is run through the HTML tokenizer of `golang.org/x/net/html`,
which follows the HTML specification for tags, quoting, comments, doctype and
raw-text elements; the scanner does not model HTML itself. A printing action
(`{{.X}}`, `{{print ...}}`) may stand only in text content outside raw-text
elements, or entirely inside a quoted attribute value. It is refused in a tag
name, an attribute name, an unquoted value, an end tag, a comment, a doctype and
in the content of any raw-text element (`<style>`, `<textarea>`, `<title>`,
`<xmp>`, `<iframe>`, `<noembed>`, `<noframes>`, `<noscript>`, `<plaintext>`,
`<script>`). `{{if}}`, `{{range}}`, `{{with}}`, `{{break}}`, `{{continue}}` and
`{{template}}` may stand in text content only. Every branch must start and end
in text: a branch that ends inside a tag, a comment or a raw-text element is
refused, and so is a template that does. Such a section or partial is refused
whatever the data, because the rule reads the template source.

| Guarantee | Package | Tests |
|---|---|---|
| `plugin.js` is not linked without the grant (the stylesheet is not gated) | `internal/render` | `TestPluginScriptNeedsGate` |
| Sections and partials with scripting markup follow the grant, and the two exceptions stay allowed | `internal/render` | `TestPluginMarkupScriptsFollowTheGrant`, `TestPluginMarkupRefusedWithoutTheGrant`, `TestPluginMarkupExceptionsStayAllowed` |
| The sample render is a defence-in-depth layer: the template is also rendered once on empty data and the output scanned. The source rule refuses `<scr{{"ipt"}}>`-style sources first, so the gate's call to it is not pinned by any test. Five representative printing-action sources passing both layers is a canary, not a proof | `internal/render` | `TestPluginSampleRenderStopsWhatTheSourceScanCannotSee`, `TestPluginSampleRenderCanary` |
| A printing action only in text or in a quoted attribute value, never in a name, unquoted value, comment, doctype or raw-text element (`<style>` included); control structures, `{{break}}`/`{{continue}}` and `{{template}}` only in text; every branch starts and ends in text. Tag boundaries come from `x/net/html`'s tokenizer | `internal/render`, `internal/packages` (Postgres) | `TestPluginMarkupActionsNeedTheGrant`, `TestPluginMarkupActionsPartials`, `TestPluginMarkupActionsDefineEnd`, `TestHostilePluginJS_SectionsAndPartialsNeedTheGrant` |
| Hostile plugin JS, sections and partials stay out without the grant, end to end | `internal/packages` (Postgres) | `TestHostilePluginJS_NotLinkedWithoutGrant`, `TestHostilePluginJS_SectionsAndPartialsNeedTheGrant` |
| A stored grant counts only while the plugin's current manifest still asks for `site.scripts` | `internal/packages` (Postgres) | `TestStaleSiteScriptsGrantDoesNotCount` |
| A script source added through the head hook needs the grant | `internal/pluginhost` | `TestHeadScriptSrcNeedsSiteScripts` |
| The admin shows the grant in the permission prompt and leaves it unticked on a fresh install | `internal/admin` | `TestPluginPromptWarnsAboutSiteScripts`, `TestFreshInstallLeavesSiteScriptsUnticked` |

Community packages are refused `site.scripts` and `builder.island` (grant and
render), because both put JavaScript in the site origin. There is no island
iframe. A page that carries a live community script would be served in a CSP
sandbox (opaque origin, Layer B). **Themes are trusted and outside Layer B**: a
theme controls every public template and its JavaScript, so a community theme
runs in the site origin. Install themes only from sources you trust.

Limits you should know about:

- The markup scanner is new and has had **no independent adversarial review
  yet**. The tests above are the project's own. Treat `site.scripts` as the
  real boundary: grant it only to plugins you would let run code on your site.
- With more than one replica, a revoke on one replica clears that replica's
  page cache at once, but the others keep serving cached pages (and the
  `plugin.js` link) until the entries expire or those replicas reload plugins.
  Plan for that window after a revoke. The details are in `docs/PLUGIN_API.md`.
  No test covers this window: it is a documented limit, not a guarantee.

### Packages: signers and trust

A plugin or theme is a package signed with an ed25519 key. Packages are loaded
only when their signer is trusted, or when the owner has explicitly allowed
unsigned packages.

| Guarantee | Package | Tests |
|---|---|---|
| A package is refused when its signature, digest or format is wrong | `internal/packages` | `TestVerifyRefusals`, `TestSymlinkIsRefused`, `TestDeclaredSizeIsNotTrusted` |
| An unknown signer is refused until its key is added | `internal/packages` | `TestDefaultTrustRejectsUnknownSignerUntilAdded` |
| Removing a trusted key disables its packages at the next load | `internal/packages` (Postgres) | `TestARemovedTrustedKeyDisablesItsPackages` |
| A change of signer or a downgrade needs the owner's confirmation, and a signer change clears the plugin's grants | `internal/packages` (Postgres) | `TestDowngradeAndSignerChangeNeedTheOwnersConfirmation`, `TestSignerChangeResetsThePluginsGrants` |
| Unsigned packages need an explicit opt-in behind four gates (acknowledgement, typed site address, duration, password), and a timed one lapses by itself | `internal/admin`, `internal/packages` | `TestAllowUnsignedEachGateAlone`, `TestAllowUnsignedCheapGatesComeBeforeThePassword`, `TestUnsignedPackagePromptWarnsAndShowsProblem` |
| The first-party key is trusted by default and can be switched off | `internal/config` | `TestFirstPartyKeyIsAlwaysTrusted`, `TestFirstPartyTrustCanBeSwitchedOff`, `TestTrustedKeysAreValidatedAtBoot` |

The unsigned opt-in confirms the owner's password only. It does not ask for the
second factor, by design: the four gates above are the whole check.

A signature says who built a package, not that it is harmless. **Themes are
trusted code**: a theme's templates run with the full power of the template
engine, so install only themes you would put in your own repository. There is
no test for that statement; it is the model.

### Updates and releases

| Guarantee | Package | Tests |
|---|---|---|
| A manifest or record that was tampered with, signed by the wrong kind of key, expired, too long-lived or issued in the future is refused | `internal/release` | `TestVerifyTamperedManifest`, `TestVerifyTamperedRecord`, `TestVerifyWrongKeyManifestSignedByReleaseKey`, `TestVerifyWrongKeyRecordSignedByManifestKey`, `TestVerifyExpired`, `TestVerifyTooLongLived`, `TestVerifyFutureIssued` |
| A binary whose hash or size differs from the signed record is refused | `internal/release` | `TestVerifyArtifactSHA`, `TestVerifyArtifactSizeMismatch`, `TestVerifyArtifactOverCap` |
| A manifest for another channel is refused | `internal/release` | `TestVerifyRefusesOtherChannel`, `TestVerifyChannelMatrix` |
| The embedded keys load as documented, and no private key is tracked in the repository | `internal/release` | `TestEmbeddedTrust`, `TestNoTrackedPrivateKeys` |
| The supervisor writes downloaded files and reads its state without following symlinks | `internal/supervisor` | `TestWriteSyncedRefusesSymlink`, `TestStateRefusesSymlink` |

### Admin isolation

A script on a public page runs in the same origin as the admin. Three layers keep it from reading or
driving the admin: it cannot read an admin page, send an admin request that passes the guard, or send
editor messages to the page builder. Two paths used to let it do the last two. Both are closed and
have attack tests (below). What remains is listed under "Residual risks". Admin paths on this page are written `<admin>/...`: every install has its own
admin address, which `hatamoto admin-url` prints. The tests and the browser job pin it to `/admin`.

| Guarantee | Package | Tests |
|---|---|---|
| Layer A: an admin document loads only on a real top-level navigation, with sealed headers, so a script on a public page, first-party or not, cannot fetch, frame or open it and read the result | `internal/admin` | `TestGuardNavigationOnly`, `TestGuardHeaders`, `TestSealWriterForcesHeaders`, `TestGuardServiceWorkerRefused`, `TestGuardTokenOnNonNavigation`, browser job `test/admin-attack/run.sh` (attacks AT-01 to AT-22) |
| Layer A: admin frames (plugin pages, previews) load only with a one-time ticket bound to the session | `internal/admin` | `TestGuardFrameTickets`, `TestTicketRoundTripIsOneTime`, browser job |
| Layer A: an unsafe admin request needs a token bound to the signed-in session; a token from another session is refused | `internal/admin` | `TestGuardUnsafeNeedsToken`, `TestTokenReplayAcrossSessions`, browser job |
| The JSON API cookie (`ENABLE_COOKIE_AUTH=true`) is its own session kind with its own HttpOnly cookie, and the admin cookie never authenticates `/api/v1`. Unsafe cookie requests need a token bound to that API session (an HMAC of the cookie value, returned by login, which page script cannot compute and is never given) and must be same-origin; a forged `c` cookie plus header is refused. Reads are not token-gated: while a browser holds an API cookie session, a same-origin page script can read `/api/v1` as that user (see "Residual risks") | `internal/middleware`, `internal/auth`, `internal/server` | `TestAuthMiddleware_AdminCookieNeverAuthenticatesAPI`, `TestAuthMiddleware_APICookieBearerAndFlag`, `TestCSRF`, `TestCSRFEmptyKeyNeverMatches`, `TestAPISessionKindValidators`, `TestAPICookieWiring`, browser job (AT-03b, which is AT-03 with `ENABLE_COOKIE_AUTH=true`) |
| Layer A: no admin state sits in `localStorage`, so a page script cannot read or plant recovery copies; they live on the server | `internal/admin` | `TestRecoveryHandlers`, browser job |
| Layer A: the edit link on a public page (`/-/edit?id=...`) redirects only a real navigation with a session, and every other request gets the admin 404, so a script cannot probe it | `internal/admin` | `TestEditLink` |
| Editor previews and the builder canvas run no page script: a plugin script, island or theme script is not emitted into them, the frame is a CSP sandbox that allows only the bridge script (by hash), and the bridge talks to the builder over a `MessagePort` the host hands over once, so a `postMessage` from any other script in the frame is ignored | `internal/render`, `internal/server` | `TestEditorViewsEmitNoPageScripts`, `TestPublicViewKeepsPageScripts`, `TestRenderPreviewSandboxAndNoPluginScript`, `TestRenderPreviewInlineScriptBlockedByCSP`, `TestCanvasBridgeScriptHashMatchesInjectedBody`, `TestBridgePortHandshakeIsFirstWins`, browser job (AT-22, the canvas drive attack) |
| Layer B: a page that carries a live community script is served in a CSP sandbox without `allow-same-origin`, so the script has an opaque origin and sends no cookie to the admin | `internal/server` | `TestWriteHTMLSandboxCSP`, `TestRealRenderHandlersSandboxFollowsCommunityLive`, `TestPublicResponsesSendNoCOOP`, `TestAdminResponsesSendCOOP`, browser job (B-1 to B-3, with the test-only `hatamoto_qa` build) |
| Layer C: the admin address is per install, and every other path answers the one standard 404, with or without a session | `internal/server` | `TestStandardNotFoundMatchesRandom404`, `TestScannerTableByteIdentical`, `TestScannerTableIgnoredWithValidSession`, `TestRobotsNeverRevealsNonDefault`, `TestHealthPublicIsPlainOK`, `TestInterceptThrottlesIdentifiedClients`, browser job (S-1, about 200 well-known paths on a random address) |

The browser job `test/admin-attack/run.sh` runs in CI on every push to main and on pull requests that
touch `backend/internal/{admin,adminpath,session,recovery,packages,config,httpx,pluginhost,render,middleware,auth,server}/`,
`backend/go.mod` or `backend/go.sum`, the admin web assets, the harness itself or the CI workflow. It starts a real binary, grants a self-built plugin
`site.scripts`, and tries every attack from a public page. Each attack has a positive control, so a
probe that cannot see success is reported as inconclusive and fails the run. Layer B is checked with the `hatamoto_qa` build, which
forces the sandbox on every public page, because no community package can hold `site.scripts` today.

Residual risks, stated plainly:

- **Layer A is not an origin boundary.** The admin and the public site share one origin. Layer A is
  built from browser rules (fetch metadata, headers, tickets, tokens). A browser bug or a missed
  rule weakens it, and a plugin with `site.scripts` still runs with the visitor's page privileges.
- **One admin endpoint that forgets a rule reopens the hole.** That is why the rules are one
  router-level guard, not per-handler checks, and why the browser job attacks the real binary.
- **Phishing and password-manager autofill are not stopped.** Neither is attacked in the job.
  Two-factor sign-in or WebAuthn is the control.
- **Community `builder.island` gap.** A builder island is a same-origin
  `<script type="module">` (`internal/render`, `islandTags`). The Layer B sandbox applies only when
  the plugin also holds a live `site.scripts` grant (`internal/packages`, `CommunityScriptsLive`), so
  a community island with no such grant would run unsandboxed in the site origin. Today community
  packages are refused `builder.island` and `site.scripts`, and that refusal is the only barrier.
  This is why the policy stays closed: community `site.scripts` is not allowed, and the refusal must
  not be lifted until islands are moved under Layer B or split from that flag.
- **Plugin service worker.** A script with `site.scripts` can register a service worker scoped to its
  own asset directory, `/p/<plugin>/assets/` (AT-15 shows it registers). It cannot control `/` or the
  admin, but it persists in visitors' browsers for requests under that path.
- **Shared `localStorage` and `sessionStorage`.** A page script shares both with the admin. Nothing
  secret sits there, but the admin reads a few keys: the builder note
  (`hm-builder-note:<entity>`, shown as an info message in the builder) and the callout dismissal
  keys (they hide admin callouts). A script can plant or set them, which can spoof that text or hide
  a callout. It cannot read admin content.
- **JSON API cookie (`ENABLE_COOKIE_AUTH=true`).** Once a browser holds an API cookie session
  (from `POST /api/v1/auth/login`), any page script on the same origin (`site.scripts`, a theme
  script, or injected script) can read and call `/api/v1` as that user, because the browser sends
  the HttpOnly cookie with its requests. Reads need no token. Writes need the session-bound token,
  so a browser client must keep `csrf_token` out of `localStorage` and `sessionStorage`, which a page
  script shares. The AT-03b control shows the read. The admin session cookie does not authenticate
  the API, so signing in to the admin alone opens nothing here. Leave the flag off unless a browser
  client needs it.
- **Layer C is not an access control.** The mechanism is public and only the address is secret. A
  leaked address gives an attacker nothing without a session, but the address is not a password.

### Network edge: client address, proxies and CSRF

| Guarantee | Package | Tests |
|---|---|---|
| `X-Forwarded-For` is believed only from a trusted proxy (loopback by default; the right-most untrusted hop wins; a spoofed left entry is ignored) | `internal/middleware` | `TestGetIP_RightmostUntrusted`, `TestGetIP_AllPrivateChainFallsBack` |
| With `HATAMOTO_TRUSTED_CDN=cloudflare`, `CF-Connecting-IP` is believed only behind a Cloudflare edge address (a forged header from any other peer is ignored), and the CDN ranges never count as trusted proxies for scheme or host. The preset trusts any Cloudflare address, so the origin should accept traffic only from Cloudflare; a firewall on Cloudflare's ranges still admits other accounts, while Authenticated Origin Pulls (zone-level, your own certificate) restricts traffic to your own zone; a Cloudflare Worker can only present a Cloudflare address | `internal/middleware` | `TestGetIP_CDN`, `TestGetIP_CDNDoesNotWidenProxyTrust`, `TestClientIPInfo`, `TestGetIP_ConcurrentSwap` |
| `HATAMOTO_TRUSTED_CDN` accepts `cloudflare` or `off` and rejects any other name | `internal/config` | `TestTrustedCDNParsing` |
| `HATAMOTO_TRUSTED_PROXIES` accepts CIDRs and addresses and rejects anything else | `internal/config` | `TestTrustedProxiesParsing` |
| The site's base URL follows forwarded headers only from trusted proxies | `internal/server` | `TestDetermineBaseURLHonoursForwardedHeadersOnlyFromTrustedProxies` |
| Unsafe admin methods need a session-bound synchronizer token. The admin sets no readable CSRF cookie; the legacy `hatamoto_csrf` cookie is only cleared | `internal/admin` | `TestCSRFOnUnsafeMethods`, `TestGuardUnsafeNeedsToken` |
| The JSON API's optional cookie (`ENABLE_COOKIE_AUTH=true`) needs a CSRF token on unsafe methods; API-key calls are exempt and cookie calls are not | `internal/middleware` | `TestCSRF`, `TestCSRFEmptyKeyNeverMatches`, `TestCSRFSkippedForAPIKeyAuthType` |
| A CSRF token is never read from a URL, including multipart forms, in the `X-CSRF-Token` header or as the first multipart field | `internal/admin`, `internal/admin/designcheck` | `TestMultipartCSRFNeverFromQuery`, `TestImportWizardCSRFNeverFromQuery`, `TestUILintMultipartFormsPutCSRFFirst` |

**Docker note.** Behind a proxy container, the server sees the proxy's bridge
address, not loopback. Set `HATAMOTO_TRUSTED_PROXIES` to that address, or the
server uses the proxy's address as the client address (and the rate limits,
logs and crawler analytics see one visitor). The reverse-proxy page of the
docs has the `docker network inspect` command.

### Uploaded files (media)

| Guarantee | Package | Tests |
|---|---|---|
| Files are opened through `os.Root`: traversal and symlinks that leave the upload directory are refused | `internal/media`, `internal/server` | `TestOpenFile_RefusesTraversalAndSymlinkEscape`, `TestServeMedia_RefusesTraversalAndSymlinkEscape` |
| A type that is not on the media allowlist is served as an octet-stream attachment with a sandbox policy, so uploaded HTML or SVG cannot run on the site's origin | `internal/server` | `TestServeMedia_HTMLOnDiskIsAnAttachment` |
| Import refuses bad names and symlink escapes, and skips (with a warning) files whose content is not on the media allowlist, such as HTML or SVG | `internal/portability` | `TestMediaStageRefusesBadNamesAndSymlinkEscapes`, `TestMediaStageSkipsNonAllowlistedTypes` |
| A symlinked directory inside the upload directory fails loudly instead of leaking | `internal/media` | `TestUpload_SymlinkedDirInsideUploadDirFailsLoudly` |

### Sign-in, setup and keys

| Guarantee | Package | Tests |
|---|---|---|
| Password hashing (argon2id, 64 MB per hash) runs at most four at once; later attempts wait and give up when the client goes | `internal/auth` | `TestHashPassword_BoundedConcurrency`, `TestHashPassword_CancelledWaiterReturnsPromptly` |
| The setup code is at least 12 characters once normalised and not trivial; a fresh install with a weak code does not start; an install that already has users only warns | `internal/config`, `internal/server` | `TestSetupCodeValidation`, `TestSetupCodeFreshInstallWithShortCodeIsRefused`, `TestSetupCodeUpgradedInstallWithShortCodeStarts` |
| The setup wizard needs the code and works once | `internal/admin` | `TestSetupRequiresCodeAndIsOneShot` |
| An API key's secret is shown once | `internal/admin` | `TestAPIKeysSecretShownOnce` |
| Key scopes are separate: `write:entities` does not imply the site-structure scopes, and each site-structure MCP write tool needs its own scope | `internal/auth`, `internal/mcpserver` | `TestSiteWriteScopes`, `TestSiteWriteScopesAreSeparate` |
| The MCP endpoint refuses browser requests from origins that are not the site's or listed, and counts every request against the key's rate limit | `internal/mcpserver` | `TestOriginValidation`, `TestOriginWithoutPublicOriginRefusesBrowsers`, `TestRateLimitPerRequest` |
| An API key reaches only the routes its scopes allow, and a key whose creator lacks the editor role cannot write | `internal/middleware` | `TestAuthOrAPIKeyAllowlist`, `TestAuthOrAPIKeyRejectsCreatorWithoutEditRole` |

### Outbound connections of the server itself

| Guarantee | Package | Tests |
|---|---|---|
| `HATAMOTO_AGENT_RANGES=off` schedules no download of AI-crawler IP ranges, and crawler verification still runs on stored ranges and reverse DNS | `internal/server`, `internal/agents` | `TestAgentRangesSwitch`, `TestRunVerifyRunsWhenRangeRefreshIsOff` |
| The update source must be https (plain http only to a loopback address, when explicitly allowed) | `internal/updater` | `TestNewFetcherURLRules`, `TestFetcherRefusesBadArguments` |

### Traffic analytics (beacon and country lookup)

| Guarantee | Where | Check |
|---|---|---|
| The public beacon input is sanitised and capped: strings are lowercased, limited to a safe character set and truncated, the body is size limited, and the referrer is reduced to a host | `internal/analytics` track handler | handler tests in `internal/analytics` |
| The GeoIP database path is set only by the operator through the `HATAMOTO_GEOIP_DB` environment variable, never by an admin-writable setting, so it cannot become a file-read primitive. The file is read once at boot, size capped, and never fetched from the network | `internal/config`, `internal/analytics` | geo tests in `internal/analytics` |
| The engagement opt-out is owner-only and audit-logged | `internal/admin` | traffic settings tests in `internal/admin` |
| No new personal data is stored: the traffic tables hold counts under coarse keys, and a schema test fails if a column named like an identifier (ip, ua, user agent, referrer, visitor, session, cookie) is added | `internal/analytics` | schema test in `internal/analytics` |

## The compile cache holds native code

The plugin host compiles each module to native code and caches the result in
`HATAMOTO_PLUGIN_CACHE_DIR`, by default `<HATAMOTO_DATA>/cache/wasm`. The directory is created with mode 0700
(`TestCompileCacheWarmLoad` in `internal/wasmhost` checks that). Whoever can
write to that directory can make the server run native code, as whoever can
replace the binary can. Protect the data directory like the binary: only the
service user may write to it.

## Not yet covered by a test

These statements are true of the code today but no test pins them. They are
listed so that nobody mistakes them for enforced guarantees.

- An API key is stored as the SHA-256 of its secret, never the secret itself.
- Themes are trusted code (above).
- The multi-replica cache window after a `site.scripts` revoke (above).

## Explicit non-goals

- **A malicious owner or admin.** Admins can install code and change anything.
- **A malicious theme.** Themes are trusted code.
- **Another process on the same machine with the service user's rights**, or
  anyone who can write to the binary, the data directory or the environment file.
- **Plugins that you granted `site.scripts`**, `http.fetch.any` or other broad
  capabilities: the owner's grant is the boundary.
- **Denial of service by volume.** There are rate limits and caps, not a
  guarantee of availability under load.
- **A compromised Postgres superuser or the host operating system.**
- **Owner opt-outs.** Allowing unsigned packages, setting
  `HATAMOTO_PLUGIN_DB_ALLOW_PUBLIC_OBJECTS=1`, switching off first-party trust
  (`HATAMOTO_TRUST_FIRST_PARTY`) or adding trusted keys moves the boundary to
  what the owner chose.
- **Third-party plugins' own bugs.** Report sandbox escapes and capability
  bypasses here; everything else belongs to the plugin's author.
- **Hardware side channels** in the WASM runtime (beyond what the runtime itself
  mitigates).

## Check this page

Every test named above must exist. From the repository root, this prints the
number of distinct names in this page and the number `go test -list` finds
(with the integration tag, so the Postgres tests count); the two must be equal:

```sh
cd backend
names=$(grep -o '`Test[A-Za-z0-9_]*`' ../docs/SECURITY_MODEL.md | tr -d '`' | sort -u)
echo "$names" | wc -l
go test -tags=integration -list "^($(echo $names | tr ' ' '|'))\$" ./... | grep '^Test' | sort -u | wc -l
```
