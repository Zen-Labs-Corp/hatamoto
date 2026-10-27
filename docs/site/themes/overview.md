---
title: Understand how themes work
nav_title: How themes work
description: See which files a theme is made of, how Hatamoto picks the template for each page, and how to install, preview and switch themes.
next: your-first-theme.md, layout.md
---

A theme decides how your site looks: the HTML of every page, its CSS and its images. This guide walks you through the files a theme is made of and how Hatamoto picks the template for each page, so you know where to make a change. It ends with the screen where you install, preview and switch themes.

## Before you start

- A running Hatamoto site you can sign in to as an owner.
- Optional: a copy of the Hatamoto source code. The built-in theme lives in `backend/internal/render/themes/default/` and is the best example to read next to this guide.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. **Look at the files of a theme.** A theme is a folder with a `theme.json` file and up to five folders:

   ```text
   my-theme/
     theme.json     the manifest: name, version, optional settings
     layouts/       one template per kind of page
     partials/      smaller templates reused inside layouts
     sections/      the blocks editors place on pages
     schemas/       the fields of each block
     assets/        CSS, scripts, fonts and icons
   ```

   The **manifest**, `theme.json`, gives the theme's `name` and `version`, and can describe settings the site owner fills in. A **layout** is the template for a whole page: a page, a post, an archive, the homepage, search, and the not found and error screens. A **partial** is a smaller template reused inside layouts, such as the header, the navigation and the footer. [Layout and template hierarchy](layout.md) lists every file.

2. **Look at a section.** A **section** is a block an editor can place on a page, such as a hero or a list of questions. Each one is a template in `sections/` plus a **schema** in `schemas/`, a JSON file that lists the fields the editor fills in. The admin builds the editor's form from the schema. [Add a section](add-a-section.md) builds one.

3. **See how a missing file is found.** A theme does not have to be complete. Hatamoto looks for every file in two places, in order: your theme first, then the built-in `default` theme. This order is the **lookup chain**, and the first place that has the file wins. A theme that ships only `layouts/page.html` and a stylesheet still renders posts, archives and search, because the missing files come from the built-in theme. The built-in theme ships every layout, every required partial and 23 section types.

4. **See how a layout is picked.** For each page, Hatamoto has a list of layout names to try, most specific first. A blog post (content type `post`) tries `single-post`, then `single`, then `page`. Hatamoto tries the whole list in your theme before it moves to the built-in theme. So if your theme ships only `page.html`, posts render with your `page.html`, not with the built-in `single.html`. The table of every page kind is in [Layout and template hierarchy](layout.md).

5. **Open the themes screen.** In the admin, open **Theme > Themes** (`<admin>/theme/themes`). It lists the built-in theme and every installed theme. The live one carries a **Live** badge.

6. **Install a theme.** Themes arrive as a **package**, one `.hmp` file that holds the whole theme. **Signing** proves who made it and that nobody changed it. Choose **Install** next to a theme under **Available themes**, which come from a registry (a place that publishes packages), or choose **Upload theme** for a file you have. Installing never changes the site. By default Hatamoto accepts only signed packages from keys it trusts. [Package and share a theme](packaging.md) explains how.

7. **Preview it.** Choose **Preview** on a theme card. A new tab shows your real site in that theme, in your browser only, for one hour. Visitors still see the live theme. Drafts never show in a preview. To end it early, open `/?hm_theme_preview=off` on your site.

8. **Activate it.** Choose **Activate**. Hatamoto first lists the section types your content uses that the new theme does not have, with a count for each. You can still go on with **Activate anyway**. The new theme then serves every visitor at once, with no restart. To switch back, activate the other theme, or the built-in one, the same way.

   The same actions work from a terminal on the server. They need `DATABASE_URL` set:

   ```bash
   hatamoto package install my-theme-1.0.0.hmp
   hatamoto theme activate my-theme
   ```

   ```text
   theme my-theme is now live
   a running server applies it within a few seconds; no restart is needed
   ```

   Unlike the admin, `theme activate` refuses while sections are missing unless you add `--force`. Run `hatamoto theme activate default` to go back to the built-in theme.

## Check it worked

You can now answer these questions about any theme:

- Which file renders a blog post? (`layouts/single-post.html`, `single.html` or `page.html`, in your theme first, then in the built-in theme.)
- Where does a page get its header? (The `header` partial, from your theme if it has `partials/header.html`, else from the built-in theme.)
- Which theme is live? (The one with the **Live** badge on **Theme > Themes**.)

If a theme you installed does not appear as live after **Activate**, look at its card: a line starting **Not used:** gives the reason, for example a signing key the server no longer trusts. The checks Hatamoto runs on every theme are listed in [Engine details](engine-details.md).
