---
title: Settings reference
nav_title: Settings reference
description: Every Settings and Theme screen of the admin, who may open it, and what each option, limit and default does.
---

This page lists every screen under **Settings** and **Theme** in the admin, who may open it and what each option does. For step-by-step tasks, see [Add media, menus, forms and redirects](media-menus-redirects.md) and [Invite people and create API keys](users-and-api-keys.md).

## The Settings map

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

Settings holds everything about the site as a whole rather than one entity. Each section has its own address under `<admin>/settings/`, and each tab is a separate address. A section address redirects to the first tab your role may open.

| Section | Tab and address | Who |
|---|---|---|
| General | `general` | owner |
| Email | `email` | owner |
| Privacy | `privacy` | owner |
| Tracking | `tracking` | owner |
| Media | `media` | owner, editor |
| Entity models | `types`, `taxonomies` | owner |
| Forms | `forms`, `submissions` | owner, editor |
| Forms | `spam` (Spam protection) | owner |
| Redirects | `redirects` | owner, editor |
| Internal linking | `linking` | owner, editor |
| Users | `users` | owner |
| API | `api-keys` | owner |
| Webhooks | `webhooks`, `webhooks/deliveries` | owner |
| Site access | `site-access` | owner |
| Performance | `performance` | owner |
| Updates | `updates` | owner |
| Import & export | `import`, `export` | owner |

Entity types and taxonomies are described in [Entity types and taxonomies](entity-types-and-taxonomies.md). How the site looks lives in the separate Theme area, see [Theme](#theme). Editors have their own profile at `<admin>/profile`.

## General

`<admin>/settings/general` is one page with several cards, each saved on its own:

- **Identity**: site name, tagline, **site address**, optional custom domain, **language**, **time zone** and a logo address. The site address is the public origin, like `https://example.com`, and is used in links and emails. The time zone is what schedule fields and dates use.
- **Homepage**: which entity is served at `/`.
- **Email**: the **notification email** (where form messages and alerts go), and whether outgoing mail is set up, with a link to **Settings > Email**. See [Email](#email).

The business details (phone, hours, address and the rest) are not on this page. They live per language under **Settings > Locales > Business details**, where the default locale's card is your organisation. See the locales section of the admin guide.

A Hatamoto site serves one language. The Language setting declares it for markup and feeds; it does not create language-prefixed addresses. See [One site, one language](../getting-started/one-site-one-language.md).

## Media

The library is at `<admin>/settings/media`, with Grid and List views, a search box and a type filter (All files, Images, Videos, Documents). The list shows each file's name, type, size, alt text and upload date. A "Missing" badge flags images without alt text.

| Item | Detail |
|---|---|
| Upload | The **Upload** button opens a dialog, or `<admin>/settings/media/new` without JavaScript. Up to 20 files at once. |
| File size | Each file is limited by `MEDIA_MAX_SIZE_MB` (default `10`). |
| Storage | Files are stored under `MEDIA_UPLOAD_DIR` (default `./uploads`) and served from `MEDIA_BASE_URL` (default `/media`). |
| Variants | For JPEG, PNG and WebP images Hatamoto creates thumbnail and medium variants; GIF and SVG are stored as they are. |
| Upload request | A `multipart/form-data` post to `<admin>/media/upload`. Because the body is not read in full before the CSRF check, the token travels in the `X-CSRF-Token` header or as the form's first field (`_csrf`, before any file input). It is never accepted from the query string, so it does not appear in proxy logs. |
| Detail page | `<admin>/media/{id}` edits the alt text and caption, offers **Download** and **Copy address**, and deletes the file after a confirmation page. The page builder and the blog editor both warn about images without alt text. |
| Picker | Image fields, the social image and the editors open `<admin>/media/picker` as a dialog, with search and an upload option. |

## Forms and submissions

Forms are at `<admin>/settings/forms`, and the messages visitors send are at `<admin>/settings/submissions`.

A form has a name, an **Identifier** (used in the form's address; made from the name when left empty), a **Status** (Active accepts submissions; Inactive shows nothing and accepts nothing; Archived), a description shown only in the admin, and an ordered list of fields.

| Field option | Values |
|---|---|
| Type | Single-line text, email address, phone number, multi-line text, drop-down list, radio buttons, checkboxes, date, number and web address |
| Label, Field name | What the visitor sees, and the name the answer is stored under |
| Width | Full, half or third |
| Placeholder, Help text | Hints in and under the field |
| Required | The visitor must answer |
| Choices | For list types |
| Checks | Shortest answer, longest answer, a pattern the answer must match, and your own message when a check fails |

Form-level settings:

| Setting | Meaning |
|---|---|
| Button text, Success message | What the visitor sees |
| Redirect to | An optional page to send the visitor to after submitting |
| Email notifications to | Separate several addresses with commas |
| Keep submissions in the admin | Store messages in the submissions list |
| Send the visitor a confirmation email | With your own text |
| Require the spam check (captcha) | Show the captcha chosen in Spam protection and refuse submissions that fail it. If no provider is set up, a warning shows and submissions are accepted unchecked. |

The **Embed** card shows the address a hand-built form in a template posts to; fields are sent by their name. Place a form on a page with the builder's `form` widget or a contact-form section. Notification mail needs outgoing email, see [Email](#email).

The submissions list filters by form and by status (New, Read, Archived, Spam). Messages caught by the spam rules are stored as Spam, send no email and can be moved to another status if they were a false alarm. Bulk actions mark rows as read, archive them, mark them as spam or delete them (twice confirmed). Open a submission at `<admin>/settings/submissions/{sid}` to read it, change its status or email it on. **Export CSV** (`<admin>/settings/submissions/export.csv`) streams 500 rows at a time with a byte-order mark, and prefixes cells that begin with `=`, `+`, `-` or `@` so a spreadsheet will not run them as formulas.

## Spam protection

`<admin>/settings/spam` (Forms, Spam protection tab, owners only) sets up one spam pipeline that every public form shares, whether the visitor posts with JavaScript or with a plain browser post. The page has two cards.

**Captcha.** Choose a provider: Cloudflare Turnstile, Google reCAPTCHA v3 or hCaptcha. Create a site in the provider's dashboard, then paste the **site key** and **secret key** here. The site key is public and ends up in the page. The secret key is encrypted in the database with a key derived from `HATAMOTO_SECRET`, is never shown again, never goes to the browser and is left out of the audit log. Leave the field blank when saving to keep the stored key. **Test connection** asks the provider whether the saved secret is valid, so save first. reCAPTCHA v3 is invisible and fails scores below 0.5 (version 2 checkbox keys are not supported). Turnstile and hCaptcha show a widget above the submit button.

The captcha only applies to forms that have **Require the spam check (captcha)** switched on (see [Forms and submissions](#forms-and-submissions)).

**Rules.** These run on every form, with or without the captcha:

| Rule | Default | Meaning |
|---|---|---|
| Minimum time to submit | 3 seconds | Messages sent faster count as spam. 0 turns it off. Maximum 60. |
| Max links per message | 2 | More links than this count as spam. 0 blocks every link. Maximum 50. |
| Blocked words | none | Words or phrases separated by commas, up to 200. Capital letters do not matter. Entries match whole words, so `casino` does not block `casinos`. An entry that starts or ends with punctuation, such as `.ru`, also matches inside longer text. |
| Block scripts | none | Cyrillic, Chinese/Japanese/Korean, Arabic, Hebrew, Thai or Devanagari text. |
| When a rule trips | Save as Spam | **Save as Spam** or **Refuse submission**. |

**What visitors see.** With Save as Spam the visitor sees the normal success message, but the message is stored with the Spam status and no notification email, confirmation email or form hook fires. Review these in Submissions with the Spam filter and change the status of any false positive. With Refuse the visitor sees "Your message could not be accepted." and nothing is stored. A missing or failed captcha shows "Please complete the spam check and try again." and nothing is stored. A filled honeypot field still gets a fake success and stores nothing.

**If something is missing.** If a form requires the captcha but no provider is set, or the saved secret cannot be read, the form shows no widget and submissions are accepted, with the rules still applied. A warning appears on this page and on the form editor, so a misconfigured site keeps taking enquiries. If the provider cannot be reached when a visitor submits, that counts as a failed check. After you change `HATAMOTO_SECRET` the saved secret key is unreadable and the page asks you to enter it again.

The timing check uses a signed token that the page fetches when it loads, so cached pages never contain one. A post without a token skips only the timing rule. Saving this page clears the page cache. Without JavaScript a captcha form cannot be submitted, because the widget needs a script. The captcha script and verification addresses are added to the public Content Security Policy only while a provider is set up and its secret is readable, see [Hardening](../operate/hardening.md). This does not replace edge protection: if you run behind a CDN or firewall, also rate limit `POST /forms/*` and `POST /api/v1/public/forms/*` there.

## Redirects

Redirects are data, edited at `<admin>/settings/redirects` by owners and editors. The section has three tabs: **Redirect rules**, **404 log** (`<admin>/settings/redirects/404`) and **Explain a URL** (`<admin>/settings/redirects/explain`).

| Item | Detail |
|---|---|
| A rule | **From** (the source path), **To** (the target) and **Type**. |
| Types | `301: moved permanently` (the default), `302: moved temporarily`, `307: temporary, keeps the request method`, `308: permanent, keeps the request method` |
| On or off | A rule can be turned off without deleting it (**Redirect is on** in the form, or **Turn off** in the list). |
| Bulk actions | Delete, Turn off, Turn on |
| List columns | From, To, Type, Hits, State |
| 404 log | Addresses visitors requested that did not exist, with hits, last seen and where the visitor came from. Each row offers **Create redirect**, **Explain**, **Ignore** and **Remove**. |
| Explain a URL | Shows every step for a path and which rule matched. |

How rules are matched, wildcard rules, the redirects Hatamoto writes by itself and the old admin addresses are described once, in [Redirect rules](../migrate-contribute/redirect-rules.md).

## Internal linking

`<admin>/settings/linking` (owners and editors) helps you link your own pages together without editing each one.

- **Rules** map a phrase to a target page: the phrase, the target, optional link text, a priority (1 to 100) and the number of links per page. A rule can be previewed before saving, switched on or off, or deleted in bulk.
- **Suggestions** (`<admin>/settings/linking/suggestions`) finds places in existing content where a rule applies and inserts the link when you confirm.
- **Equity** (`<admin>/settings/linking/equity`) shows how many internal links each page receives, and each row opens the list of inbound links.
- **Bulk import** (`<admin>/settings/linking/import`) loads many rules at once.

Inserting a link or changing a slug rewrites the affected links and clears the page cache.

## Users

To invite someone step by step, follow [Invite people and create API keys](users-and-api-keys.md).

`<admin>/settings/users` lists users with search, a role filter (All roles, Owners, Editors), sorting and the last sign-in date.

| Role | Can do |
|---|---|
| Editor | Manage content: entities, media, menus, forms, redirects, linking, blocks, templates, analytics |
| Owner | Everything an editor can, plus users, settings, themes, plugins, API keys and entity types |

There are two ways to add someone:

- **Invite** (`<admin>/settings/users/invite`): enter an email address and a role (Editor is preselected). The invitation link is valid for 7 days. It is shown once in a banner on the users list and, when email works and a site address is set, also emailed. The person enters a name and chooses their own password at `<admin>/accept`. The **Pending invitations** table shows the expiry and offers **Send a new link** and **Revoke**.
- **Create** (`<admin>/settings/users/new`, **Add with a password instead** in the invite form): set the password yourself. Passwords need at least 12 characters.

On a user row you can **Edit** the name, email and role, send a password reset (**Reset password**, a one-time link valid for one hour, shown once and emailed when possible, at most three per user per hour) or **Delete** the user. Deleting asks for confirmation, and the user's content stays. The last owner cannot be deleted or demoted: the form tells you to make someone else an owner first.

From a shell on the server you can create or reset a user without the admin:

```bash
hatamoto user create --email you@example.com --name "Your Name" --role owner --password-stdin
hatamoto user reset-password --email you@example.com --password-stdin
```

Flags are `--email` (required), `--password` or `--password-stdin` (prefer the latter, because `--password` is visible in the process list), and for `create` also `--name` and `--role` (`owner` or `editor`, default `owner`). Without a password the command generates one and prints it. The full command list is in the [CLI reference](../operate/cli-reference.md).

## API keys

To create a key step by step, follow [Invite people and create API keys](users-and-api-keys.md).

`<admin>/settings/api-keys` (**Settings > API**) creates keys for scripts, integrations and the CLI. Each key has a **Name**, an **Expires** choice (Never, In 30 days, In 90 days or In a year) and **Access**, the scopes that limit what it may do. At least one scope is required.

| Scope | Label | Allows |
|---|---|---|
| `read:entities`, `write:entities` | Read entities, Write entities | Read or change content |
| `write:media`, `write:menus`, `write:redirects`, `write:terms` | Write media, Write menus, Write redirects, Write terms | Site-structure writes, kept separate so a publishing key never gains them |
| `read:reports` | Read reports | Unlocks the MCP insight tools |
| `read:citations`, `write:reports`, `read:query_suites`, `write:query_suites` | | Reserved: no route checks them today, so they grant nothing |
| `*` | Everything (full access) | Everything |

| Rule | Behaviour |
|---|---|
| Showing the key | Shown **once**, right after creation, on a page that is not cached. Creating it is a POST, a redirect, then a GET, and the secret waits two minutes in memory for that one GET, so reloading cannot mint a second key. |
| Sending the key | In the `X-API-Key` header. |
| Rights | A key acts with editor rights for the user who created it, limited to its scopes. It stops working when that user is no longer an owner or editor. |
| Allowed routes | Only a fixed list of API routes accepts keys. A bad, revoked or expired key gets HTTP 401 `{"error":"invalid api key"}`; a route outside the key's scopes gets HTTP 403 `{"error":"forbidden"}`. |
| Rate limit | A key over its rate limit gets HTTP 429 with `Retry-After: 60`. |
| Row actions | **New secret** (replaces the secret; the old one stops working), **Revoke** and **Delete**, each with a confirmation page. |
| Where to use keys | From servers: the session JSON API is same-origin only, with no CORS (only the public forms and page-view endpoints answer a sandboxed `Origin: null` page). |

Keys can also be created on the server with `hatamoto apikey create --name <name> --scopes <a,b>`; see the [CLI reference](../operate/cli-reference.md).

## Webhooks

`<admin>/settings/webhooks` sends a signed `POST` to an address you choose when content changes, and keeps a delivery log at `<admin>/settings/webhooks/deliveries`. Owners only. See [Webhooks](webhooks.md).

## Site access

`<admin>/settings/site-access` (owners only) chooses who can see the public site. It has four modes:

| Mode | What visitors get |
|---|---|
| Open | The normal site. |
| Coming soon | A themed page with status 200, `noindex`, and no caching. |
| Maintenance | The same page with status 503 and a `Retry-After` header (15 minutes, 1, 6 or 24 hours, your choice), so search engines come back later instead of dropping pages. |
| Password | A page with a password form, status 401. One shared password for the whole site, not per page. |

The heading and message are plain text for the default language of the site; empty fields use a standard text. Your theme can restyle the page by shipping `layouts/coming-soon.html`; without it the built-in layout is used.

What always stays the same, in every mode:

- **You still see the real site.** Anyone signed in to the admin sees the live pages, so you can check your work. Those responses are marked `private, no-store` so a CDN never keeps them. The sign-in cookie is `SameSite=Strict`, so if you arrive from another site by a link, you see the gate once; open the site again.
- **Open paths.** The admin address, `/health`, `/ready`, `/api/`, `/p/` (plugin pages), `/.well-known/`, `/media/`, `/theme/assets/` and the site icons are never gated. Public form submits are gated like pages. Plugin routes under `/p/` stay open, so a plugin that serves a public page there is still reachable.
- **Robots.** `/robots.txt` always answers and says `Disallow: /` while a mode is on. The sitemap, its parts, the feeds and `llms.txt` answer 404 in coming soon and password mode, and 503 with `Retry-After` in maintenance mode.
- **Caches.** The page cache is cleared whenever you save, and gate pages are never cached. IndexNow pings are skipped while the mode is not open.
- **Password mode.** A correct password sets a signed cookie for 30 days (`HttpOnly`, `SameSite=Lax`, `Secure` on https). Setting a new password signs every visitor out. Wrong guesses are limited per client address (429 with `Retry-After`). The password is stored as a hash and never shown again; leave the field empty to keep it.

While the mode is not open, owners and editors see a banner on every admin page. Each change is written to the audit log with the old and new mode, never the password.

## Performance

`<admin>/settings/performance` controls the page cache and preload. Owners only. See [Performance and the page cache](performance-cache.md).

## Email

`<admin>/settings/email` (the Email section of Settings, owners only) sets up the mail server for password resets, invitations, form notifications and confirmations. The first-run wizard has the same form as its Email step, after Your site, with a Skip button. The page shows a badge ("Email is set up" or not) and which source is in use: the settings saved here or the server's `SMTP_*` environment.

| Field | Meaning |
|---|---|
| Provider | Custom, Amazon SES (with a region, default `us-east-1`) or Gmail. SES and Gmail fill in the host. |
| Host, Port | Host is used with Custom. Usually 587 with STARTTLS or 465 with TLS (SSL). |
| Security | STARTTLS, TLS (SSL) or None. None is only accepted for `localhost`, `127.0.0.1` or `::1`. |
| Username, Password | Blank for no sign-in. The password is never shown. Leaving it blank keeps the saved one only if host and username are unchanged. |
| From address, From name | Sender. A blank name uses the site name. |

**Save** connects, negotiates TLS, signs in and tries MAIL FROM, with a 10 second limit. No mail is sent. If a step fails you see the SMTP code and message and nothing is saved. **Send a test email** (`<admin>/settings/email/test`) mails you with the settings in use. **Use the server's SMTP_* settings instead** (`<admin>/settings/email/reset`) deletes the saved settings and goes back to the environment.

| Rule | Behaviour |
|---|---|
| Precedence | Saved settings replace the `SMTP_*` environment as a whole, not field by field. |
| Password | Encrypted with `HATAMOTO_SECRET`. If the secret changes, Hatamoto uses the environment and the page asks you to "Re-enter your SMTP password". |
| Several replicas | Changes apply at once on the replica you used; the others pick them up within 30 seconds. |
| Kill switch | `EMAIL_DISABLED=true` still switches all sending off. |

Links in emails use the configured site address (`PUBLIC_BASE_URL`, else the site address under **Settings > General**), never request headers. Without a site address, or without email set up, nothing is emailed: invitations and resets created by an owner show the one-time link on screen to pass on, and "forgot password" points to `hatamoto user reset-password`. Reset links last an hour. See [Set up email](../operate/email.md) for the full guide.

## Privacy: cookie consent

`<admin>/settings/privacy` (the Privacy section of Settings, owners only) turns on a cookie banner for your visitors and announces their choice to the scripts on your pages. It is **off by default**: until you turn it on, your public pages are exactly what they were before.

| Field | Meaning |
|---|---|
| Show a cookie banner | The switch. Off by default. |
| Show the banner to | **Every visitor**, or **Visitors in the EU and UK**. The second mode asks the browser to check `GET /api/v1/public/consent/region`, which reads the country header your CDN adds (`CF-IPCountry` or `CloudFront-Viewer-Country`). The page HTML is the same for every country, so it stays cached. A missing header shows the banner; a country outside the EU, the UK, Iceland, Liechtenstein and Norway does not, and those visitors are treated as having allowed everything. There is no GeoIP database: without a CDN header, use Every visitor. |
| Banner title, Banner text | Your own wording, up to 80 and 500 characters. The button and dialog labels are built in (English). |
| Categories to ask about | Analytics, Marketing and Functional. A category you untick is not offered and is reported as not allowed. Necessary is always on. |

A visitor's choice is stored for 12 months in the `hm_consent` cookie, a necessary cookie that holds only the choice. It is asked again after 12 months, or when you change the set of categories. Add `data-hm-consent-open` to any link or button, for example in your footer, to let visitors reopen the dialog; without one, a small "Cookie preferences" button is added to the page.

Under the form, a table shows how many visitors chose Accept all, Reject all or Custom each day for the last 30 days. Only these daily totals are stored. There is no IP address, user agent or visitor id, and no record of who chose what. Saving the page clears the page cache so the change shows at once.

**Upgrade:** nothing changes until you turn the switch on. The migration adds two tables and nothing else.

## Tracking: analytics, ad and chat tags

`<admin>/settings/tracking` (the Tracking section of Settings, owners only) loads third-party tags on your public pages from one place. It is **off by default**: until you turn a tag on, your page HTML and its Content-Security-Policy header are byte-identical to before.

Each of the 13 tags has a switch and an ID field: Google Analytics 4, Google Tag Manager, Google Ads, Meta Pixel, Microsoft Clarity, Microsoft UET, LinkedIn Insight, TikTok Pixel, Pinterest Tag, Tawk.to, Crisp, HubSpot and Google AdSense. The page shows the format of each ID, the consent category of the tag and the hosts it adds to the page policy. An ID that does not match the format shows an error next to the field, linked from the summary at the top, and nothing is saved. A tag that is on needs an ID.

| Field | Meaning |
|---|---|
| Do not load tags for signed-in users | On by default. When a browser holds the `hm_member` cookie, which is set at sign-in and cleared at sign-out, no tag loads. Pages are cached and identical for everyone, so the browser makes this choice. |
| A tag's switch and ID | Turns the tag on with its ID. Analytics tags are in the Analytics category, ad tags in Marketing, chat tags in Functional. |

How it works:

- One script, `/theme/assets/hm-tags.<hash>.js`, is added to public pages. It loads the tags that are on, and each one waits for the visitor's choice in Settings > Privacy when the banner is on. Nothing is loaded in the editor or in previews.
- The page policy allows only the hosts of the tags that are on.
- Google Tag Manager also adds a `noscript` frame, only when the consent banner is off (with the banner on, a category it does not ask is never allowed).
- After a form is sent, the page dispatches one `hm:conversion` event on `document` with the submission id in `detail.id`. Google Analytics 4 receives `generate_lead` and Meta Pixel `Lead`, with the submission id as the event ID.
- Every change is recorded in the audit log with the tags turned on, turned off or edited. IDs are not written there. Saving clears the page cache.

**Upgrade:** the Google Tag Manager and Google Analytics plugins are replaced by this page. At the next start, an ID stored in a plugin signed by the first-party key is copied here (turned on only if the plugin was installed, on, allowed to add scripts and granted that permission, so an upgrade never starts a tracker that was not running), the plugin is uninstalled and the audit log records `tracking.import_plugin`. A **Moved into core** notice shows once on the page until you dismiss it. A plugin with these names that is not first-party signed is left alone, and so is a plugin whose ID is not a valid ID for the tag (it is not uninstalled and its settings are kept): enter its ID here, then remove it from Plugins. The migration adds one table.

## Visitor consent API

When the banner is on, a script `/theme/assets/hm-consent.js` is the first element inside `<head>` of every public page (not in the editor preview). Scripts on your pages, plugin scripts and tag managers read the visitor's choice from it:

- `window.hm.consent.get()` returns `{necessary: true, analytics, marketing, functional, decided}`. Before a decision `decided` is `false` and the three categories are `false`. A visitor outside the region in EU and UK mode gets `decided: true` and every category `true`, with no banner, no cookie and no count.
- `hm.consent.allows(category)` returns a boolean for `analytics`, `marketing`, `functional` or `necessary`.
- `hm.consent.open()` opens the preferences dialog. `hm.consent.version` is the version of the category set.
- The event `hm:consent` is dispatched on `document` on every change, and once at load when a valid stored choice exists. Its `detail` has the same shape as `get()`. The event can fire before your listener exists, so call `get()` first and then listen.
- **No banner means no `hm.consent`.** When the banner is off the script is not on the page and `hm.consent` is undefined. Treat undefined as every category granted.
- **Sandboxed pages do not remember the choice.** When a community plugin adds a script to your public pages, the browser runs them in a sandbox that blocks cookies. The banner still shows on every page and the visitor's choice applies to that page view only.
- **Consent Mode v2.** The script defines `window.dataLayer` and `gtag` if they are missing and pushes `consent default` first, before any other script, with `ad_storage`, `ad_user_data`, `ad_personalization`, `analytics_storage`, `functionality_storage` and `personalization_storage` set to `denied` and `security_storage` to `granted` (in EU and UK mode with `wait_for_update: 500`). On every change it pushes `consent update`. Analytics maps to `analytics_storage`; Marketing to `ad_storage`, `ad_user_data` and `ad_personalization`; Functional to `functionality_storage` and `personalization_storage`.
- **Cookie.** `hm_consent` holds `encodeURIComponent` of JSON `{v, t, a, m, f}` (category-set version, Unix seconds, and 0 or 1 for analytics, marketing, functional), with `Max-Age=31536000`, `Path=/` and `SameSite=Lax` (`Secure` on https). It is mirrored in `localStorage["hm_consent"]`; the cookie wins.
- **Counter.** After a choice the browser posts `{"choice": "all" | "none" | "custom"}` to `POST /api/v1/public/consent`, limited per IP, which adds one to the day's total.

The banner only gates and announces. It does not load or block vendor scripts by itself: a script that sets analytics or marketing cookies has to wait for `hm.consent.allows(...)` or the Consent Mode signal.

### Email log

`<admin>/settings/email/log` (the Email log tab, owners only) lists every email the server sent or dropped, 25 to a page, newest first. The **Status** filter shows Sent, Failed or Suppressed (email off). The Kind column names the mail, for example Form notification, Password reset or `Plugin: <id>`. Open an entry to see its recipients, time, error text and message. **Resend** sends a stored message again to the same recipients and adds a linked entry. Password reset, invitation and admin address mails never keep a body ("Body not stored"), and a body removed by retention shows "Body no longer kept"; neither has Resend.

On **Settings > Email**, **Keep message bodies (days)** (default 30) and **Keep log entries (days)** (default 90) set the retention, each 1 to 365 with bodies no longer than entries. See [Set up email](../operate/email.md#the-email-log).

## Import and export

- **WordPress import** (`<admin>/settings/import`) is a five-step wizard: Source, Scan report, Options, Run and Report. Scanning and importing run as background jobs, and the page refreshes itself while a job runs. You can cancel or resume a run. See [Migrate and contribute](../migrate-contribute/index.md).
- **Site export & import** (`<admin>/settings/export`) is informational. It shows the commands to run on the server, because a full export belongs in a shell:

```bash
hatamoto export site.tar.gz
hatamoto export site.tar.gz --no-submissions
DATABASE_URL=postgres://new-host/hatamoto hatamoto import site.tar.gz
```

Passwords are never exported, so set one for each person who needs to sign in on the new host.

## Updates

`<admin>/settings/updates` shows the running version and the automatic update policy: the **mode** (Automatic, Notify only or Off), the release **channel**, a **maintenance window** in the site time zone, and a **defer** setting that holds releases back for 0 to 14 days (security fixes still install). You can check now, apply a staged update or remove a pin. A setting that is fixed by an environment variable is shown locked with the name of the variable. The mechanics are described in [How updates work](../updates/index.md).

## Theme

The **Theme** item in the sidebar (`<admin>/theme`) has one section per page:

| Section | Address | Who |
|---|---|---|
| Themes | `<admin>/theme/themes` | owner |
| Colours and fonts | `<admin>/theme/colours` | owner |
| Design system | `<admin>/theme/design-system` | owner |
| Style presets | `<admin>/theme/presets` | owner |
| Layout parts | `<admin>/theme/parts` | owner |
| Blocks | `<admin>/theme/blocks` | owner, editor |
| Templates | `<admin>/theme/templates` | owner, editor |
| Menus | `<admin>/theme/menus` | owner, editor |

Owners switch and configure **Themes** (activate, per-theme settings, uninstall), set the brand colours, fonts and corners under **Colours and fonts**, and shape the builder under Design system, Layout parts and Style presets, described in the [Editor reference](builder-reference.md). **Templates** lists the layout templates saved from the builder and older section templates. Activating a theme shows a confirmation page that lists any section types the new theme lacks. Themes install inactive; owners can preview one at `<admin>/preview/theme/{name}` before activating it.

### Menus

A menu has a **Name** and a **Location**: Header, Footer, Sidebar or No location. Your theme decides which locations it prints, and the builder's `menu` widget can show any menu by location.

The menu editor is a tree. Each item has:

- a **Label**;
- what it **Links to**: "A page or post" (an entity picked from a search box), "A custom address" (any address or path) or "A category or tag" archive;
- an **Open in a new tab** switch.

The buttons move an item up or down, indent it under the item above, outdent it, remove it, or add a new item. Without JavaScript these are form buttons that re-render the page without saving; with JavaScript the same buttons work in place and you can also drag items (sub-items travel with their parent). **Save menu** validates and replaces the whole menu in one transaction, so a menu is saved entirely or not at all. Saving clears the page cache.

## Plugins

Owners manage plugins at `<admin>/plugins`: upload a package or browse the registry, review the permission prompt, enable or disable, update (the list shows each version, **Update to vX** for a safe update, **Review update** when an update asks for new permissions, and **Update all** for every update that needs no review), and open each plugin's own Settings and Permissions tabs. Writing plugins is covered from the [plugin overview](../plugins/overview.md).
