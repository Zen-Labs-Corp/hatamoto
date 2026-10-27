---
title: Move your site to Hatamoto
nav_title: Move your site
description: Plan the move of an existing site into Hatamoto, pick the right import and switch over without losing old links.
next: wordpress-import.md, export-import.md, redirects-after-migration.md
---

This guide helps you plan the move of an existing website into Hatamoto. You pick the right way to bring your content in, learn the order of the work, and switch your domain over without breaking the links that search engines and other sites already have.

## Before you start

- A Hatamoto site that is installed and that you can sign in to as an **owner**, the role that can do everything, including imports. If you do not have one yet, follow the Start here path in [Getting started](../getting-started/index.md).
- Access to your old site: its admin, or a shell on its server.
- Control of your domain's DNS settings, the records that send visitors to a server. You change them at the very end.
- An afternoon. The import itself is quick, but checking the result takes time.

## Steps

### Pick the way in

1. Find out what your old site runs on, and pick the matching way in:

   | Your old site | Use | What it brings |
   |---|---|---|
   | WordPress | [Import from WordPress](wordpress-import.md) | Pages, posts, categories, tags, authors, images, menus, SEO titles and descriptions, and redirects for every address that changes |
   | Another Hatamoto site | [Export and import](export-import.md) | The whole site's content in one **archive**, a single compressed file you copy to the new server |

   Any other system has no import. Copy the content by hand in the admin, then add redirects as in the "Keep old links working" steps below.

2. Write down the addresses of your most visited pages. Your old site's statistics are a good source, and so is its **sitemap**, a file that lists your pages for search engines (often at `https://example.com/sitemap.xml`). You use this list to check the result.

### Bring the content in

1. Keep the old site online at its usual address, and stop changing it if you can. The import reads from it, and visitors still use it.
2. Run the import on the new Hatamoto site, following the guide for your source. A WordPress import can be repeated later to pick up new posts. An archive import needs an empty site.
3. Read the report at the end. It lists every page that needs a look, such as pages built with a page builder plugin, whose layout does not come across.
4. Open your list of important pages on the new site and compare them with the old ones. Fix titles, images and menus in the admin.

### Keep old links working

1. Check that every old address leads somewhere. The WordPress import does this for you with its **URL audit**, a check that requests each old address on the new site and lists those that do not answer. For an archive import the addresses stay the same, because the archive holds your redirects and your address settings.
2. Add a **301 redirect**, a permanent forward from an old address to a new one, for each address that still fails. [Keep old links working after a move](redirects-after-migration.md) shows how.

### Switch over

1. If you imported from WordPress more than a day ago, run the import once more to pick up recent changes. Do this before you edit pages in Hatamoto, because a new run overwrites those edits unless you ask it to keep them.
2. Change your domain's DNS records to point at the new server. Old visitors and search engines now reach Hatamoto.
3. For the first weeks, open **Settings > Redirects** in the admin and its **404 log** tab. It lists addresses visitors asked for that do not exist, so you can add a redirect for each one that matters.

## Check it worked

Open three or four old addresses from your list in a private browser window, using your real domain. Each one shows the right page, either directly or after a single forward to its new address. The address bar shows the new address when a forward happened.

Then search the 404 log for your most important old addresses. If one appears there, the forward is missing: create a redirect for it from the log row with **Create redirect**.

If the import refuses to start with `database is not empty`, you are using the archive import on a site that already has content. The archive import needs an empty database: use a fresh install, or bring the content in with the WordPress import instead.
