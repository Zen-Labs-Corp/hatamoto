---
title: Accessibility statement
nav_title: Accessibility statement
description: The accessibility level the Hatamoto admin aims for, how it is tested, what is checked by hand and the gaps we know about.
---

This page states what the Hatamoto admin aims for, how that is tested and where it falls short. For how to use the admin with a keyboard and a screen reader, see [Accessibility and security](accessibility-and-security.md).

## Overview

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

| | |
|---|---|
| Target | WCAG 2.2, level AA, for every admin screen, in light and dark colours and at 1440 and 390 pixels wide |
| Scope | The admin under `<admin>`, and the default theme's navigation. Plugin admin pages and themes you install from elsewhere are their authors' responsibility. |
| Status | Pre-1.0. The checks below pass on the screens we test, but we have not had an outside audit. |

## How it is tested

| Check | What it covers | Where it runs |
|---|---|---|
| Structure checker | A checker with no browser (`backend/internal/admin/a11y`) runs over every golden page and partial: names, landmarks, headings, ids, tables, dialogs and token contrast. | Every `go test` run, so in CI |
| Admin rules in Chrome | `scripts/admin-qa/consistency.mjs` checks the admin's design rules on real pages, including field borders and focus rings. | CI, in the admin-ui job |
| axe | `scripts/admin-qa/a11y.mjs` runs axe-core with the WCAG 2.0, 2.1 and 2.2 A and AA rule sets on every screen at 1440 light, 1440 dark and 390. | On a maintainer's machine, with `scripts/admin-qa/run-local.sh`. **Not in CI:** the script currently stops on the row menu of the backups screen, so it is not a gate yet. |
| Keyboard | `scripts/admin-qa/keyboard.mjs` drives menus, popovers, dialogs, tabs and sortable lists in Chromium, Firefox and WebKit. | A maintainer's machine |
| By hand | Screen readers (VoiceOver, NVDA), real Safari on macOS and iOS, IME input, touch drag and the editors. The checklist is `docs/QA.md`. | Before each release |

The last full axe run recorded in `docs/QA.md` had 0 violations in 113 scans.

## Known gaps

- **Resting field borders are lighter than 3:1.** The border of a text input, select or textarea at rest uses the token `--border-field`, which is below the 3:1 contrast WCAG 1.4.11 asks for. This is a deliberate relaxation. The field is still identified by its label, its fill and its position, the hover and focus states keep a 3:1 border and a focus ring, and axe still passes. The rule is in `docs/ADMIN_UI_RULES.md` (UI-11). Checkboxes, switches and secondary buttons keep the full-contrast border.
- **axe is not a CI gate yet.** See the table above.
- **Screen reader and browser coverage is manual.** Nothing in CI runs a screen reader or real Safari.
- **No outside audit.** Treat the statement as the project's own claim.

## Report a problem

If something in the admin does not work with your keyboard or assistive technology, open an issue in the [Hatamoto repository](https://github.com/Zen-Labs-Corp/hatamoto/issues) with the screen and the tool you use. Accessibility problems in the admin are bugs, not feature requests.
