---
title: Preview and develop a theme
nav_title: Preview and dev
description: Work on a theme with live updates on a development site, then preview it on your real site before anyone else sees it.
next: packaging.md, layout.md
---

In this guide you set up the loop you use while you build a theme: you save a file and the development site shows the change. Then you preview the finished theme on your real site, over your real content, before you make it live for visitors.

## Before you start

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

- A theme folder, such as `my-theme` from [Build your first theme](your-first-theme.md).
- A Hatamoto site for development whose `ENVIRONMENT` setting is not `production`, and its database address (`DATABASE_URL`).
- For the preview part: an owner account on your real site, and a packaged theme ([Package and share a theme](packaging.md)). Only owners can install, preview from the Themes screen and activate. Once the theme is installed, an editor can preview it too by opening `<admin>/preview/theme/<name>` with the theme's name.

## Steps

### Work on a development site

1. Set the database address of the development site in your terminal:

   ```bash
   export DATABASE_URL=postgres://hatamoto:hatamoto@localhost:5432/hatamoto
   ```

2. Start the dev loop:

   ```bash
   hatamoto theme dev ./my-theme --dev
   ```

   ```text
   watching ./my-theme; Ctrl-C to stop
   14:02:11  pushed my-theme v1.0.0 (dev, unsigned)
   ```

   The command packs the folder as an unsigned development package, installs it and, on the first push, makes it the live theme. Add `--no-activate` to install it but keep the current live theme. `--dev` confirms you are on a development site; you can set `ENVIRONMENT=development` in your shell instead.

3. Edit a file and save it. The command checks the folder every half second and pushes again when a file is added, removed or changed. The running site applies each push within about a second, with no restart.

4. Read each new line in the terminal. Every push runs the same checks as an install: the package must be valid, the theme must load, and every kind of page (page, post, homepage, archive, category, author, search, not found and error) must render with test data. A bad template call or a missing partial shows here, not on your visitors:

   ```text
   14:02:40  NOT PUSHED: package: the theme does not render: search view: render search: html/template:layouts/search.html:1:31: no such template "no-such-partial"
   ```

   After a `NOT PUSHED` line the site keeps the last version that worked. Fix the file and save again.

5. Keep build files out of the theme folders. A theme uses `theme.json` and the folders `layouts`, `partials`, `sections`, `schemas` and `assets`. The command skips hidden files, `node_modules`, and any file or folder at the top level that a package cannot hold. Everything inside `assets` is packed and served, so if you use a build tool, keep its sources in a separate folder.

6. Open your pages in the page editor too. The preview of the page in the editor uses your layouts and sections, so you can check that editors can still click and change each text.

### Preview on your real site

1. Install the packaged theme on your real site. Installing never changes what visitors see. [Package and share a theme](packaging.md) shows how.

2. Open **Theme > Themes** (`<admin>/theme/themes`) and choose **Preview** on the theme's card. A new tab opens your site in that theme.

   The preview lasts one hour and shows only in your browser. It shows published content only, never drafts. Visitors keep seeing the live theme, and preview pages are never stored in the page cache.

3. Browse the pages that matter: the homepage, a post, an archive, a page with many sections, the search page.

4. End the preview by opening `/?hm_theme_preview=off` on your site, or wait for the hour to pass.

5. When it looks right, choose **Activate** on the card.

Important: the preview link works like a password for that hour. Anyone you send it to can view the theme's design over your published content. Signing out does not end it; opening `/?hm_theme_preview=off` does.

## Check it worked

- On the development site, a save shows a new `pushed` line and the change is on the page after a reload.
- On the real site, the preview tab shows the new theme while a private browser window still shows the live one.

If the dev command stops with `this database belongs to a production server, which refuses dev packages`, the database is a production site's. Production sites never load development packages. Use a development copy, or package and sign the theme.

If a push never shows on the page, check the terminal for `NOT PUSHED`, then check that `DATABASE_URL` points at the database of the site you are looking at.

The flags of `hatamoto theme dev` and the other theme commands are listed in [Package format and commands](package-format.md). The preview addresses the admin uses are described in [Preview API](preview-api.md).
