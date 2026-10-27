---
title: Accessibility and security
nav_title: Accessibility, security
description: How the admin works with a keyboard and a screen reader, how its screens behave, and how it protects accounts, sessions and forms.
---

This page lists how the admin behaves for keyboard and screen reader users, how its screens work without JavaScript, and the rules that keep accounts and sessions safe. It is the detail behind [Take a tour of the admin](tour.md). For the server around the admin, see [Hardening](../operate/hardening.md).

## Overview

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

The admin is a set of server-rendered pages: the server builds each screen as HTML. Links and forms are ordinary browser navigation. Each change is a real form post that answers with a redirect and a short message. A few features (the editors, drag and drop, charts) are added on top with small scripts. There is no build step and no separate front-end application: the `hatamoto` program serves everything under `<admin>`.

## Signing in and first visit

| Address | What it does |
|---|---|
| `<admin>` | Opens the admin. A signed-out visitor is sent to `<admin>/login`. |
| `<admin>/login` | Sign-in form. After sign-in you land on `<admin>/entities`. |
| `<admin>/setup` | First-run wizard on a fresh install: account, site, email, look, content, done. It needs the one-time setup code from the server log, or the value of `HATAMOTO_SETUP_CODE` if you set one. See [First run](../getting-started/first-run.md). |
| `<admin>/forgot` | Forgotten password. When outgoing email is set up, the account with that address receives a reset link. The page answers the same whether or not the address has an account. Without email, the page asks the visitor to have an owner run `hatamoto user reset-password --email <address>` on the server. |
| `<admin>/reset` | Where a reset link lands. |
| `<admin>/accept` | Where an invitation link lands: the new user enters a name and a password. |
| `<admin>/profile/complete` | Shown once after sign-in while your person profile has no name or no photo. |

The profile step blocks every admin page except the profile pages, sign-out, static files, the media upload and picker (the photo field needs them) and the light or dark switch. Nothing is stored for this: "complete" is worked out from the person record.

## How screens behave

- **Layout.** The sidebar has Entities, Theme, AI analytics, Plugins and Settings. People (`<admin>/people`) sits under Entities, and the Entities item stays highlighted there. Your avatar (a link to `<admin>/profile`), the light or dark button and the sign-out button sit at the bottom; each is a real form with a label and a tooltip.
- **Second menus.** Settings and Theme each have a second sidebar. A section address such as `<admin>/settings/entity-models` redirects to the first tab your role may open, and `<admin>/settings` itself redirects to the first section you may open. The full list is in the [Settings reference](settings-users-email.md).
- **Narrow screens.** Below 640 pixels wide the sidebar becomes a top bar, and secondary lists (the Settings and Theme sections, the entity types, the plugins) turn into a select box with a **Go** button, which works without a script.
- **Page widths.** Narrow for forms, wide for lists, full for the page and blog editors, which use their own layout without the sidebar.
- **Tables keep their state in the address.** Search text, sort, filters and the page number are query parameters (`?q=...&sort=...&dir=...&page=2`), so a filtered view can be bookmarked or shared.
- **Destructive actions never fire on one click.** Delete links open a confirmation page, or the browser's confirm dialog when JavaScript runs. Bulk deletes ask twice.
- **Validation errors** re-render the form in place with an error summary at the top (HTTP 422). Success redirects with a banner that fades into a toast.
- **Secrets are shown once.** A new API key, a webhook signing secret, an invitation link or a reset link appears on a page that is never cached and is not shown again.
- **Roles.** An editor who opens an owner-only address gets a "Not allowed" page (HTTP 403).
- **Dark mode** follows the button in the sidebar footer and is remembered in a cookie, so there is no flash on load.
- **Page transitions** use the browser's cross-document view transitions where supported, and the admin prerenders same-site admin links on hover. Both are optional and respect the reduced motion preference. Editors, exports and form targets are never prerendered.

## Keyboard and accessibility

The admin is built to be used with a keyboard and a screen reader, and the project checks it automatically and by hand; the [Accessibility statement](accessibility.md) says what is tested, and where, and lists the known gaps.

- Every icon-only button has an `aria-label`. Form fields have an id and a name, and errors are linked to their field.
- Focus rings are always visible. Colour pairs are checked for WCAG contrast in both light and dark themes.
- Status messages use `role=status`. The Navigator tree in the page builder is a real ARIA tree with one tab stop and arrow-key movement.
- The page builder has a full shortcut set, listed in the [Editor reference](builder-reference.md); press `?` in the builder to open it. The blog editor's shortcuts (`Ctrl` or `Cmd` plus `S` saves, `K` adds a link and `Shift+F` toggles focus mode) are listed there too.
- Everything works without JavaScript: the entity form, the blog editor (the body becomes a plain HTML text area), the builder outline form, menus, forms, filters and confirmations.

## Security model

| Area | Behaviour |
|---|---|
| Sessions | The session cookie (`__Host-s`, or `s` on plain HTTP) is HttpOnly and SameSite=Strict. A session lasts at most 30 days and expires after 7 days without use; an idle session is revoked, not only refused. Signing in revokes the session the browser presented. **Sign out on all devices** on `<admin>/profile` ends every session of the account, including the current one. |
| `Secure` cookie flag | On over TLS, when a trusted proxy sends `X-Forwarded-Proto: https`, or when `ADMIN_COOKIE_SECURE` asks for it (default on when the environment is production). A missing header never switches it off. |
| Trusted proxies | `X-Forwarded-*` headers are believed only from the addresses in `HATAMOTO_TRUSTED_PROXIES` (comma-separated CIDRs; the default is loopback only; Docker deployments set the bridge gateway). |
| CSRF | Forms carry a `_csrf` field matched against the token bound to the session (the admin sets no CSRF cookie), and script requests send `X-CSRF-Token`. Cross-site `Origin` and `Sec-Fetch-Site` values are refused. Multipart uploads put the token in the `X-CSRF-Token` header or as the form's first field (`_csrf`, before any file input), because the body is not read in full before the check. The token is never accepted from the URL. |
| Content Security Policy | Every admin response carries its own strict policy: no `unsafe-inline`, a nonce on each inline script and style, and `frame-ancestors 'self'`. The admin therefore uses no inline event handlers or inline styles. |
| Emailed links | Reset and invitation links are built only from the configured site address (`PUBLIC_BASE_URL`, otherwise the site address under **Settings > General**), never from request headers. When email works but no site address is set, nothing is emailed and the server logs a warning. Invitation and reset links that an owner creates in **Settings > Users** are always shown to that owner once as well. |
| Tokens | Reset and invitation tokens are single use, stored hashed and never logged. A reset link lives one hour and an invitation seven days. Reset links for one user are limited to three per hour. |
| Sign-in errors | Read the same for a wrong password, an unknown address and a locked account. |
| JSON API | Same-origin only (no CORS). The public forms and page-view endpoints answer a sandboxed page (`Origin: null`) only, without credentials. Programs use API keys, sent in the `X-API-Key` header; see [Settings reference](settings-users-email.md). |
