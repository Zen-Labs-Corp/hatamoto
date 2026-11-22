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
| Editors, other roles and API keys | Do what their role or key scopes allow | Reach owner-only screens, or act outside a key's scopes |
| Themes | Everything the template engine can do: a theme is trusted code like the binary | Be installed from an unknown signer (refused until the owner trusts the key) |
| Plugins | Only the capabilities the owner granted, inside a WASM sandbox | Anything else: see "Plugins" |
| The update channel and registry hosts | Nothing on their own: every release is checked against keys compiled into the binary, and every package against the first-party key, the keys in `HATAMOTO_TRUSTED_KEYS`, or (developer packages, only with the owner's switch on) the signer key registered in the developer index | Shipping code that is not signed |
| Visitors | Read public pages, submit public forms (including a file on a form that has a file field) | Everything else |
| Reverse proxies | Tell the server the client address, only when listed in `HATAMOTO_TRUSTED_PROXIES` (loopback by default) | Anything from an unlisted address |

The SEO and terms key scopes (`read:seo`, `write:seo`, `write:terms`) work only while the key's creator is an owner, also through `*`. Keys minted with `hatamoto apikey create --as <editor>` do not get them.

The default role for accounts created by single sign-on may be any of the six built-in roles, so an owner can have new sign-ins start as Read-only.

## Guarantees and the tests that enforce them

### Plugin sandbox

A plugin is a WebAssembly module run by the host. The module has no network, no
file system and no host access except through capabilities the owner granted.

| Guarantee | Package | Tests |
|---|---|---|
| A guest cannot open a connection, even when its manifest lists a host | `internal/wasmhost` | `TestNoNetwork` |
| A guest cannot read a host file | `internal/wasmhost` | `TestNoFilesystem` |
| Memory is capped, also under concurrent attempts | `internal/wasmhost` | `TestMemoryBombHitsCap`, `TestMemoryCapRejectsTinyModuleBudget`, `TestMemoryCeilingIsTwiceMaxPages`, `TestConcurrentMemoryBombsBounded`, `TestTablesAreBounded` |
| A call that runs too long is stopped, with or without a caller deadline, and so is the instantiation of a module (its start section included) at load | `internal/wasmhost` | `TestTimeoutIsEnforcedWithoutCallerDeadline`, `TestPerHookTimeouts`, `TestSlotWaitIsBoundedWithoutCallerDeadline`, `TestStartSectionIsBounded` |
| Result and message sizes from the database are capped | `internal/pluginhost` | `TestPluginConnConfigBoundsServerMessages`, `TestPluginDBHugeRowIsBounded_Integration` |
| Guest log output is truncated and rate limited | `internal/wasmhost` | `TestGuestLogsAreTruncatedAndRateLimited` |
| A capability is denied until the owner grants it, and a grant the manifest does not request is refused | `internal/pluginhost` | `TestDeniedUntilGranted`, `TestGrantMustBeRequested` |
| A host call request over 1 MiB is refused | `internal/wasmhost` | `TestHostCall` |
| Guest logging, `mail.send` and `http.fetch` are rate limited per plugin; other host calls are not rate limited | `internal/pluginhost` | `TestHostCallLimits` (log), `TestFetchRateCapPerPlugin` (`http.fetch`), `TestMailSendRateLimitPerPlugin` (`mail.send`: per recipient, per plugin) |
| A plugin's `entity.before_save` filter is registered only while the owner holds `entities.filter`; plugins installed earlier keep it once (recorded at the first start, granted when each next loads); installs and updates afterwards are not kept, and a new ask in an update needs confirmation | `internal/pluginhost`, `internal/packages` (Postgres) | `TestBeforeSaveNeedsEntitiesFilterGrant`, `TestGrandfatherEntityFilterOnce`, `TestGrandfatherEntityFilterFailsClosed`, `TestEntityFilterKeptForDisabledPluginEnabledLater`, `TestEntityFilterKeptUnionsStoredGrants`, `TestEntityFilterNotKeptForInstallAfterSnapshot`, `TestEntityFilterNotKeptForUpdateAfterSnapshot`, `TestEntityFilterKeptPluginMayStartAsking`, `TestEntityFilterHooksFollowStoredGrant`, `TestUpdateAskingEntitiesFilterNeedsConfirmation` |
| Community and developer packages cannot carry tracking tags | `internal/packages` (Postgres) | `TestInstallRefusesTagPackFromRestrictedOrigin` |
| A standing approval cannot add a translation link | `internal/changeset` (Postgres) | `TestStandingUpdateRefusesNewTranslationLink` |
| A plugin's admin page runs in a sandboxed iframe, its bridge carries only granted capabilities, and the camera is opened by core's page, never by plugin code, only after the owner granted `ui.allow:camera` | `internal/admin` | `TestPluginUIPageIsASandboxedIframe`, `TestBridgeCarriesOnlyGrantedCapabilities`, `TestBridgeIsRateLimitedPerUserAndPlugin`, `TestPluginPageCameraPolicyFollowsTheGrant` |

The camera follows the same rule. The plugin page's iframe keeps its sandbox and
Content-Security-Policy, so plugin code cannot open a camera. Core's trusted admin
page does it when the page calls `hatamoto.camera.capture()`, and only for a plugin
whose manifest asks (`ui.allow`) and whose owner ticked `ui.allow:camera`. The admin
page sends `Permissions-Policy: camera=(self)` only on such granted plugin pages, a
person clicks **Capture** in core's dialog, and the camera tracks are stopped when
the dialog closes. The call is refused unless the person just clicked in the page, one
dialog is open at a time, and Capture ignores clicks for half a second after the
preview appears. The camera ask is off by default, owner-only (the grant needs the
owner role), shown with a New badge on the install prompt, and not part of the registry
permission review (the index and the browse cards do not list it). Revoking the grant
takes effect on the next page load.

Comments reach plugins only through the `comments.read` permission. A plugin
that holds it hears about new, approved, spam and deleted comments, may read
approved comments of public entities, and may add to the spam score of a new
comment in the `comment.check` filter. It never gets the commenter's email, a
hash of it, the IP address, the user agent or the author's web address, no
unapproved comment through the host API, and it has no function to write or
moderate comments. The `comment.check` filter fails closed: an error, trap,
timeout, reject or open circuit breaker holds the comment for moderation.

### Static hosting islands

With static hosting on, pages are served from a static origin (for example `https://static.example.net`) and the interactive parts (forms, comments, search, the beacon, public plugin routes) stay on the live origin (`https://live.example.net`).

- CORS is answered for the configured static origin only, never with credentials, and always with `Vary: Origin` in static mode.
- Other origins get no CORS, and island POSTs from them are refused in static mode.
- Comment form tokens stay bound to the visitor's network and single-use.
- The live origin redirects page GETs (`301`) to the static site, never admin, API, previews, forms, comments, search, media or assets.
- Served mode, with static hosting off, is unchanged.

Tests: `public_cors_static_test.go`, `static_origin_test.go`, `comment_form_live_test.go`, `static_live_test.go` and `static_inventory_test.go`.

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

What this does not cover: the visitor's browser. With no grant at all, a
plugin's markup and CSS (sections, partials and `assets/plugin.css`, which is
linked on every page and not gated) can make visitors' browsers load resources
from other hosts: an `<img src="https://...">`, a CSS `url()`. The public
page CSP allows images from any https host. Such a request carries the
visitor's address and the page it was made from, so an installed plugin can
track visitors. CSS attribute selectors can also test the attributes of any
element on the page and report a match through such a request. `http.fetch`
and `allowed_hosts` limit only the plugin's own (server-side) network.
Install plugins from publishers you trust. Core keeps this harmless as a
reader: public HTML carries no per-user secrets (no session, CSRF or other
token), and an owner browsing the site gets the same markup as a visitor.

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

Plugin template text is trusted for CPU and memory, like theme markup: install
only plugins you trust. A plugin `range` may iterate any value. Every plugin
`range` in a render draws on one budget of 100000 iterations per page render
(theme templates are not counted); builder blocks, layout parts and the trial render of
sections share the first budget and the page render gets a second, and the
sample render gets one budget per sample render, not per template. When the budget is spent the loop stops and the rest of every
later plugin loop in that render is left out; the page is served but not cached,
and a warning naming the plugin and the template is logged, at most once a
minute for each. The following rules are static guards against accidental
recursion, fan-out and nesting, not a bound on what a template can cost. Ranges
that start again from the content (`$.Content...`, or
`.Content...` at a template root) may nest at most two deep, counting such
ranges in a template called from inside a range; ranges over the current item's
children add no depth. Across all of one plugin's templates, `{{template}}`
calls may not form a cycle (a template that reaches itself, directly or through
others), a template may not call another plugin's templates (its partials or its section names), and no template
may expand to more than 1000 template calls. A broken call rule (a cycle, a call
to another plugin, too many calls, ranges nested too deep) is checked on every
load, including for a plugin that is already installed, together with the other
template rules, and rejects all of that plugin's templates, logged. Every section
or partial that is not registered is listed with its reason on the plugin's
admin page and in `GET /api/v1/plugins`. For a plugin without
`site.scripts` the sample render of a template is also cut off at 256 KiB of
output (the template is refused when it reaches that), and its string-building
builtins (`printf`, `print`, `println`, `slice`, `html`, `js`, `urlquery`) return
their real result cut to 4 KiB, so the compile-time render stays cheap and still
sees a literal they build; the output of plugin funcs is not represented. The
only request-time bound on template execution is the range iteration budget;
time and memory are otherwise not budgeted.

| Guarantee | Package | Tests |
|---|---|---|
| `plugin.js` is not linked without the grant (the stylesheet is not gated) | `internal/render` | `TestPluginScriptNeedsGate` |
| Public HTML is the same signed in and signed out, and no cookie value reaches it, so CSS on the page has no per-user secret to read | `internal/server` | `TestPublicHTMLCarriesNoPerUserSecrets` |
| Plugin ranges stop at the per-render budget | `internal/render` | `TestPluginRangeBudgetStopsAndDegrades`, `TestSampleRenderBoundsRanges`, `TestSampleRenderSharesOneBudget` |
| Sections and partials with scripting markup follow the grant, and the two exceptions stay allowed | `internal/render` | `TestPluginMarkupScriptsFollowTheGrant`, `TestPluginMarkupRefusedWithoutTheGrant`, `TestPluginMarkupExceptionsStayAllowed` |
| The sample render is a defence-in-depth layer: the template is also rendered once on empty data and the output scanned. The source rule refuses `<scr{{"ipt"}}>`-style sources first, so the gate's call to it is not pinned by any test. Five representative printing-action sources passing both layers is a canary, not a proof | `internal/render` | `TestPluginSampleRenderStopsWhatTheSourceScanCannotSee`, `TestPluginSampleRenderCanary` |
| A printing action only in text or in a quoted attribute value, never in a name, unquoted value, comment, doctype or raw-text element (`<style>` included); control structures, `{{break}}`/`{{continue}}` and `{{template}}` only in text; every branch starts and ends in text. Tag boundaries come from `x/net/html`'s tokenizer | `internal/render`, `internal/packages` (Postgres) | `TestPluginMarkupActionsNeedTheGrant`, `TestPluginMarkupActionsPartials`, `TestPluginMarkupActionsDefineEnd`, `TestHostilePluginJS_SectionsAndPartialsNeedTheGrant` |
| Hostile plugin JS, sections and partials stay out without the grant, end to end | `internal/packages` (Postgres) | `TestHostilePluginJS_NotLinkedWithoutGrant`, `TestHostilePluginJS_SectionsAndPartialsNeedTheGrant` |
| A stored grant counts only while the plugin's current manifest still asks for `site.scripts` | `internal/packages` (Postgres) | `TestStaleSiteScriptsGrantDoesNotCount` |
| A script source added through the head hook needs the grant | `internal/pluginhost` | `TestHeadScriptSrcNeedsSiteScripts` |
| The admin shows the grant in the permission prompt and leaves it unticked on a fresh install | `internal/admin` | `TestPluginPromptWarnsAboutSiteScripts`, `TestFreshInstallLeavesSiteScriptsUnticked` |

Community and developer packages are refused `site.scripts` and `builder.island` (grant and
render) and cannot carry tracking tags, because both put JavaScript in the site origin. There is no island
iframe. A page that carries a live community script would be served in a CSP
sandbox (opaque origin, Layer B). **Themes are trusted and outside Layer B**: a
theme controls every public template and its JavaScript, so a community or developer theme
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
| Developer packages need an owner switch behind three gates (acknowledgement, typed site address, password) and the signed index must list the signing key for the name; any other key is refused | `internal/admin`, `internal/packages` | `TestAllowDeveloperEachGateAlone`, `TestAllowDeveloperCheapGatesComeBeforeThePassword`, `TestDeveloperKeyMismatchIsRefused` (Postgres: `TestDeveloperInstallNeedsTheSwitchAndTheListedKey`) |
| The developer index is trusted only through its own listing key, which never verifies a package, and the first-party key never vouches for a developer package | `internal/packages` | `TestDeveloperIndexIsVerifiedAgainstTheListingKeyOnly`, `TestFirstPartyKeyNeverVouchesForADeveloperPackage`, `TestDeveloperIndexIsFilteredAgainstTheMainIndex` |
| Turning the developer switch off keeps installed packages running, blocks installs and updates, and an update signed by another key is refused even with a confirmation | `internal/packages` (Postgres) | `TestDeveloperSwitchOffKeepsInstalledPluginsAndPinsUpdates`, `TestDeveloperRowReloadsAtBootWithoutTheIndex` |
| A revoked developer key or version disables the installed package (data kept, reason stored, audited) until the registry withdraws it | `internal/packages` (Postgres) | `TestRevocationByKeyDisablesTheRowAndKeepsItsData`, `TestRevokedActiveThemeIsDeactivated` |
| The first-party key is trusted by default and can be switched off | `internal/config` | `TestFirstPartyKeyIsAlwaysTrusted`, `TestFirstPartyTrustCanBeSwitchedOff`, `TestTrustedKeysAreValidatedAtBoot` |

A developer package is signed code nobody at Hatamoto reviewed: the switch only
lets the install through when the key matches the one the index lists, and an
update must keep that key. Revocation can disable a package, it cannot vouch for one.

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
| Layer A: admin frames (plugin pages, previews) load only with a one-time ticket bound to the session. The record lives in the database (`hatamoto_frame_tickets`) so any server can spend it, once; the HMAC tag is never stored, so a database read alone cannot open a frame | `internal/admin`, `internal/frameticket` | `TestGuardFrameTickets`, `TestTicketRoundTripIsOneTime`, `TestStoreTwoServersOneWinner_Integration`, `TestFrameTicketMintedOnOneServerOpensOnAnother_Integration`, browser job |
| The cache purge URL (`HATAMOTO_PURGE_URL`) is fetched through safefetch: a private or loopback address is refused unless `HATAMOTO_PURGE_ALLOW_PRIVATE` is set | `internal/cdnpurge` | `TestWebhookDefaultClientBlocksPrivate`, `TestWebhookAllowPrivate` |
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
  Two-step sign-in is the control: an authenticator app (TOTP, one use per time step, sealed at rest),
  one-time recovery codes and passkeys (user verification required, single-use challenges). Every
  session-issuing path refuses a password alone for a user with a factor, and a failed factor lookup
  refuses the sign-in. Wrong codes share the account lock. An owner policy can require it, but a
  user who has no factor yet is held on the Security page in the admin, and the JSON login refuses a password-only session for them (403) until they set one up.
  Residual: a user with only passkeys confirms sudo mode with the password alone, passkeys are tied
  to the site address, rotating `HATAMOTO_SECRET` invalidates every authenticator secret and recovery code (run `hatamoto user reset-2fa` per user), and `hatamoto user reset-2fa` on the server removes every factor. Rotating it also signs every commenter device out and makes commenters start again at `new`. The comment confirmation link never confirms on GET, because link scanners fetch links; only a POST with `Sec-Fetch-Site: same-origin` does.
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

**Module routes.** A `module/route` mount is anonymous at the core level. The session cookie reaches its handler and core adds no CSRF protection there, so the module must authenticate its own callers and must not trust the core session cookie for state-changing requests. All module mounts share one auth rate limiter (per client address), so one busy mount can exhaust the budget of the others for that client.

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

### Form uploads

| Guarantee | Package | Tests |
|---|---|---|
| A stored upload is addressed only by a generated 32-hex id, never by a client name or path; names with `../`, separators or control characters are cleaned and the file is opened through `os.Root`, so a symlink cannot leave the store | `internal/form` | `TestFileStoreIDsAreNeverPaths`, `TestFileStoreSymlinkEscapeRefused`, `TestSanitizeName` |
| The extension must be on the accept table (no svg, html, js, xml or programs) and the file's content must match it | `internal/form` | `TestAcceptTable`, `TestCheckUpload`, `TestValidateDefinitionFile` |
| A file answer is accepted only as the server's own reference, so a client cannot point a submission at a stored file | `internal/form` | `TestValidateFileAnswer` |
| Deleting a submission or a form, and the retention purge, remove the files; a missing file is not an error | `internal/form` | `TestDeleteSubmissionRemovesFiles`, `TestDeleteFormRemovesItsFiles`, `TestPurgeExpiredSubmissionsRemovesReturnedFiles` |
| The multipart submit streams under a size cap, refuses the wrong type or size, and leaves nothing on disk after a refusal | `internal/server` | `TestFormUploadRefusals`, `TestFormUploadBodyCutOff`, `TestFormUploadFailedInsertRemovesFile` |
| Only a signed-in owner or editor can download an upload; others get 404 and no bytes; the reply is an attachment with `nosniff` and `no-store` | `internal/admin` | `TestSubmissionFileDownload`, `TestSubmissionFileAnonymousGetsNothing`, `TestSubmissionFileWrongTargetsAre404` |

Uploads are stored unencrypted under `<data>/private/form-uploads`, so the data directory's
access rules cover them. Files are checked by size and content type only; there is no virus scan
(see the non-goals).

### Sign-in, setup and keys

| Guarantee | Package | Tests |
|---|---|---|
| Password hashing is argon2id with m=19456 KiB (19 MiB), t=2, p=1 (an OWASP-listed setting). Hashes in flight are bounded to a quarter of the memory limit (1 to 4 hashes of that size; by CPU count when there is no limit), and a stored hash with more memory counts for proportionally more (a legacy 64 MiB hash counts as 4). Later attempts wait and give up when the client goes. A weaker stored hash is replaced by the current one on the user's next successful sign-in | `internal/auth` | `TestHashPassword_BoundedConcurrency`, `TestHashPassword_CancelledWaiterReturnsPromptly`, `TestGateSlots`, `TestNeedsRehash`, `TestLoginRehashesLegacyHash` |
| The setup code is at least 12 characters once normalised and not trivial; a fresh install with a weak code does not start; an install that already has users only warns | `internal/config`, `internal/server` | `TestSetupCodeValidation`, `TestSetupCodeFreshInstallWithShortCodeIsRefused`, `TestSetupCodeUpgradedInstallWithShortCodeStarts` |
| The setup wizard needs the code and works once | `internal/admin` | `TestSetupRequiresCodeAndIsOneShot` |
| An API key's secret is shown once | `internal/admin` | `TestAPIKeysSecretShownOnce` |
| Read-only users change nothing. Authors change only their own entries and media (the creator, not the byline, is the owner; an entry without a creator is changed only by owners, editors and reviewers). Contributors create and change only their own drafts and never publish, scheduling included. Reviewers edit, publish, unpublish and restore any entry but cannot create or delete. The content screens and routes sit behind the `content.write` action, separate from `site.write`, and the entity service checks every write again with the entry's `CreatedBy` and `Status`. A question about a role in general passes an empty `authz.Object`; a question about one entry or media item must pass that object. The REST write guard covers `/api/v1` and the admin guard covers the signed-in admin; MCP decides per tool with the same ceiling; routes of a compiled-in module carry their own gates, so a module must check the role itself | `internal/admin`, `internal/server`, `internal/mcpserver` | `TestContentRulesTable`, `TestContentRulesOwnerEditorIgnoreObject`, `TestAuthzAuthorOwnAndOthers`, `TestAuthzContributor`, `TestAuthzReviewer`, `TestMediaAuthzObject`, `TestAdminRolesRouteMatrix`, `TestRESTRolesRouteMatrix`, `TestReadOnlyKeysCannotWrite` |
| A key acts with at most its creator's current role: when the creator's role changes, the key follows at once, and a key made by a user with a read-only role cannot write whatever its scopes, and a key made by an author, contributor or reviewer is held to that role's content rules | `authz` | `TestKeyCreatorRoleCeiling` |
| Key scopes are separate: `write:entities` does not imply the site-structure scopes, and each site-structure MCP write tool needs its own scope | `internal/auth`, `internal/mcpserver` | `TestSiteWriteScopes`, `TestSiteWriteScopesAreSeparate` |
| The MCP endpoint refuses browser requests from origins that are not the site's or listed, and counts every request against the key's rate limit | `internal/mcpserver` | `TestOriginValidation`, `TestOriginWithoutPublicOriginRefusesBrowsers`, `TestRateLimitPerRequest` |
| A user whose access end date has passed cannot sign in, and their live admin, bearer and API cookie sessions and the API keys they created are refused on the next request, without waiting for the hourly job | `internal/auth`, `internal/admin` | `TestAccessEndedRefusesEverySignIn`, `TestAccessEndedRefusesLiveSessions`, `TestAccessEndedAPIKeys`, `TestAccessEndedCookieRefused` |
| An API key reaches only the routes its scopes allow, and a key whose creator lacks the editor role cannot write | `internal/middleware` | `TestAuthOrAPIKeyAllowlist`, `TestAuthOrAPIKeyRejectsCreatorWithoutEditRole` |
| A provider sign-in starts only from a sealed, single-use state cookie (`SameSite=Lax`, `HttpOnly`, limited to the provider's path); a missing, forged, expired, replayed or mismatched state is refused with the neutral message | `internal/admin` | `TestProviderStartRedirectsWithSealedLaxCookie`, `TestProviderCallbackRefusalsAreNeutral`, `TestProviderCallbackStateIsSingleUse`, `TestProviderCallbackExpiredStateRefused`, `TestProviderCallbackOtherProviderIDRefused` |
| A registered sign-in policy fails closed: a refusal or an error refuses admin and REST password sign-in, the automatic sign-in after a reset, passkey and provider sign-in, with the message for a wrong password. The command line password reset is not subject to it | `internal/auth`, `internal/server`, `app` | `TestSignInPolicyRefusesPasswordEntryPoints`, `TestSignInPolicyAdmitByMethod`, `TestSignInPolicyErrorFailsClosed`, `TestHandleLoginPolicyRefusesPassword`, `TestResetPasswordIgnoresSignInPolicy` |
| A provider sign-in is refused, and leaves no session, for a user whose access has ended or who is locked out, and for a user the provider returned but did not link to the identity | `internal/auth`, `internal/authbridge` | `TestIssueVerifiedSessionProvider`, `TestSignInPolicyRefusesSessionRows`, `TestSignInRefusesAnUnlinkedMapping` |
| A provider sign-in for a user with a second factor goes through the two-step page unless the provider asserts it performed one | `internal/admin` | `TestProviderUserWithFactorGoesThroughSecondStepUnlessAsserted` |
| Disabling a user through the provisioning API ends their access and revokes their sessions and API keys in the same call; the last open owner cannot be disabled | `internal/authbridge` | `TestProvisionDisableRevokesAtOnce`, `TestProvisionDisableLastOwner` |
| Single sign-on (OIDC) uses PKCE with S256 and a nonce, both kept only in core's sealed state cookie. An ID token is accepted only with a valid signature from the issuer's keys, the configured issuer and client ID as audience, a future expiry, the nonce, and `azp` equal to the client ID when present; a token from a changed configuration is refused | `internal/oidcsignin` | `TestStart`, `TestFinishHappy`, `TestFinishRefuses`, `TestFinishAfterKeyRotation` |
| Single sign-on creates an account only for a verified email in an allowed domain, and links to an existing account by email only when the owner turned that on; a refusal never links | `internal/oidcsignin`, `internal/authbridge` | `TestMapUserRefusals`, `TestMapUserExistingAccount`, `TestMapUserCreates`, `TestOIDCSignInRefusals`, `TestOIDCSignInEndToEnd` |
| The client secret is stored sealed, never shown again, and a blank field keeps it; the identity service is reached through the outbound client that refuses private addresses | `internal/oidcsignin`, `internal/server` | `TestSecretRoundTrip`, `TestSignInAdminSecret` |
| A user without a password cannot sign in with one; the attempt looks like a wrong password and does not count toward the lockout, nor does a wrong password for a user the sign-in policy refuses for password | `internal/auth` | `TestPasswordlessUserCannotSignInWithPassword`, `TestVerifyStoredEmptyHashIsWrongPassword` |

An access end date limits sign-in. It is not a containment boundary against a malicious owner: while their access lasts, an owner with an end date can still create a password reset link for another owner, install plugins and change settings. A password reset does not extend access, and a user whose end date has passed is refused after a reset as well.

A provider sign-in does not satisfy the second step on its own: it counts as a second factor only when the provider asserts that it performed one. Password re-confirmation for sensitive actions asks for a password, so a user created without one needs a password-set link, a password reset or a passkey before they can confirm such an action. A provider must link or create users by email only when the provider marks the address verified and the issuer is trusted; core requires only that the identity is linked to the user the provider returned, so an unverified address would allow account takeover. Provider sign-in is a compiled-in interface ([ADR 0010](adr/0010-sign-in-providers.md)); the one provider a standard build has, single sign-on with OpenID Connect ([Single sign-on](SSO.md)), is hidden until an owner turns it on and does not trust the identity service's own MFA claim.

A route mounted by a compiled-in module ([ADR 0012](adr/0012-module-runtime-hooks.md)) carries no core authentication and is not behind the site access gate: the module authenticates its callers. Core applies its sign-in rate limit per client address and a request body cap, and refuses prefixes core serves. A standard build mounts none.

### Outbound connections of the server itself

| Guarantee | Package | Tests |
|---|---|---|
| `HATAMOTO_AGENT_RANGES=off` schedules no download of AI-crawler IP ranges, and crawler verification still runs on stored ranges and reverse DNS | `internal/server`, `internal/agents` | `TestAgentRangesSwitch`, `TestRunVerifyRunsWhenRangeRefreshIsOff` |
| The update source must be https (plain http only to a loopback address, when explicitly allowed) | `internal/updater` | `TestNewFetcherURLRules`, `TestFetcherRefusesBadArguments` |

### Traffic analytics (beacon and country lookup)

| Guarantee | Where | Check |
|---|---|---|
| The public beacon input is sanitised and capped: strings are lowercased, limited to a safe character set and truncated, the body is size limited, and the referrer is reduced to a host | `internal/analytics` track handler | handler tests in `internal/analytics` |
| The GeoIP database path is set only by the operator through the `HATAMOTO_GEOIP_DB` environment variable, never by an admin-writable setting, so it cannot become a file-read primitive. The file is loaded at boot, reloaded when it is replaced (checked about once a minute), size capped, and never fetched from the network. The optional `HATAMOTO_GEOIP_MMAP` mode maps the file read-only; it is also operator-only and the file must be replaced by rename (new file next to it, then `mv`), never overwritten or truncated in place | `internal/config`, `internal/analytics` | geo tests in `internal/analytics` |
| The engagement opt-out is owner-only and audit-logged | `internal/admin` | traffic settings tests in `internal/admin` |
| No new personal data is stored: the traffic tables hold counts under coarse keys, and a schema test fails if a column named like an identifier (ip, ua, user agent, referrer, visitor, session, cookie) is added | `internal/analytics` | schema test in `internal/analytics` |

### Shared draft preview links

A preview link lets anyone who holds it read one draft without signing in, so the link is a bearer secret.

| Guarantee | Where | Check |
|---|---|---|
| The token is 32 random bytes; only its SHA-256 is stored, so a database leak yields no usable link. The full link is shown once and never written to audit or logs (the request log redacts the path) | `internal/previewlink`, `internal/server` | `TestPreviewShareServesTheDraft`, store tests in `internal/previewlink` |
| Expired, revoked, unknown, malformed and deleted-entity links all answer the same plain 404; publishing, archiving or deleting the entity revokes its links, so a published entity never shows draft content | `internal/server` | `TestPreviewShareRefusals`, `TestPreviewSharePublishedAnswersNotFound` |
| The response is `no-store`, `no-referrer`, `noindex`, `X-Frame-Options: DENY` and carries a sandbox policy with `script-src 'none'`; it never enters the page cache, sitemap, feeds or llms.txt | `internal/server` | `TestPreviewShareServesTheDraft` |
| Guessing is throttled per client by a rate limit; lifetime is 1 hour to 30 days and at most 10 links are active per entity | `internal/server`, `internal/previewlink` | route and store tests |
| Creating and revoking links is owner and editor only and audit-logged; `/preview` is a reserved first path segment | `internal/admin`, `internal/adminpath` | `TestSharePreviewFlow` |

### Review policy for human edits

A wrapper module can register the review stages of a human edit. The core never publishes an edit on a doubtful answer.

| Guarantee | Where | Check |
|---|---|---|
| A registered review policy that errors, or returns a stage without a name or group, holds the edit: `StagesFor` returns `ErrPolicy` and no stages (fail closed). Registration is frozen once `app.Main` starts | `review`, `internal/reviewstate` | `review_test.go` |
| An edit changeset publishes only when every one of its stages is approved, in order; a rejection ends it | `internal/changeset` | `stage_decision_integration_test.go` |
| The preview of a change waiting for review opens for the requester and for holders of `entity.review.decide`, by widening the frame check for edit changesets only; a plugin changeset keeps its `plugin.changeset.review` check, so a reviewer of entries never gains a view of a plugin's changes. Approve, send back, publish and discard each check `entity.review.decide` on the server; the requester is refused as a decider | `internal/admin` | `TestReviewPreviewDoesNotWidenPluginChangesets`, `review_queue_test.go` |
| With review on, a person who may not publish without review cannot write published content: the change is staged (`entity.review.request` replaces `entity.update` and `entity.publish`) on every surface, admin, REST, MCP and the CLI `--as`. No subject on the context is the system and is not held | `internal/entity`, `internal/reviewflow` | `review_gate_test.go`, `gate_test.go` |
| The review switch is read from the database on every gated write, so a server never lets a change through on a stale cache. A switch, authorizer or policy error refuses the write (fail closed) | `internal/reviewflow` | `gate_test.go` |
| Nobody decides their own change, whatever their role; a stage whose group is not `reviewers` cannot be decided unless a group resolver is registered, and then it is decided only when the person holds `entity.review.decide` and the resolver says yes (a resolver error or panic denies, the requester never decides, and a person outside the stage's group does not see the change in the queue or on its page); send back never discards the requester's version | `internal/changeset`, `internal/reviewflow` | `stage_decision_integration_test.go`, `decide_integration_test.go` |

### Agent publishing policy

A wrapper module can decide, per API key, whether a change to live content goes through, waits for review or is refused. Nothing registered means no key is asked.

| Guarantee | Where | Check |
|---|---|---|
| A registered agent policy that errors or panics refuses the key's write; a zero or unknown decision refuses it too, and nothing is staged. Registration is frozen once `app.Main` starts | `agentpolicy`, `internal/agentpolicystate` | `TestDecideFailsClosed`, `TestEnforcePolicyError`, `TestRegisterPanics` |
| A refusal writes one `agent_policy.deny` audit row (result `denied`, key id) and the reason, trimmed and capped at 200 characters, reaches the agent: REST 403, MCP error text. A failing audit write never changes the refusal | `internal/agentpolicystate`, `internal/server`, `internal/mcpserver` | `TestEnforceDeny`, `TestEnforceRecordPanicKeepsRefusal`, `TestDecideReasonClamp`, `TestAgentPolicyDenyEveryWritePath` |
| A key's unpublish or archive of live content, and its edit of a block, theme part or person, are asked of the registered policy; `RequireReview` refuses them as they cannot be staged. A key cannot delete entities | `internal/entity` | `TestAgentPolicyAsksBeforeUnpublishAndReuseEdit`, `TestAgentPolicyRequireReviewRefusesWhatCannotStage`, `TestAgentPolicyDenyEveryWritePath` |
| A policy's `Allow` never skips the site's review rule, and `RequireReview` stages the change with the site's review switch off | `internal/reviewflow`, `internal/server` | `TestAgentPolicyReviewMCPThenCreatorApproves`, `TestAgentPolicyReviewRESTStages` |
| A change held only by the agent policy has the requester `api-key:<key id>`: the key's creator may approve it; a change the site's review rule held keeps the creator as requester, and nobody decides their own change | `internal/reviewflow` | `TestAgentPolicyReviewMCPThenCreatorApproves` |
| With nothing registered no key is asked and no `agent_policy` row is written | `internal/agentpolicystate`, `internal/server` | `TestEnforceInactiveOrNotKey`, `TestAgentPolicyNothingRegisteredPublishesDirectly` |

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
- **Malware inside an allowed upload.** Form uploads are checked by size, extension and
  content type; there is no antivirus scan, and the file is only ever served as a download to
  signed-in owners and editors. Scan files before opening them.
- **Denial of service by volume.** There are rate limits and caps, not a
  guarantee of availability under load.
- **A compromised Postgres superuser or the host operating system.**
- **Owner opt-outs.** Allowing unsigned packages, allowing developer packages
  (unreviewed code signed by its author's key), setting
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
