---
title: Import from WordPress
nav_title: Import from WordPress
description: Copy your WordPress pages, posts, images and menus into Hatamoto from the admin, and check that every old address still works.
next: redirects-after-migration.md, wordpress-import-reference.md
---

In this guide you copy a WordPress site into Hatamoto with the import wizard in the admin. At the end your pages, posts, images, menus and authors are in Hatamoto, and you have a report that lists every old address and anything that needs a look.

## Before you start

- A Hatamoto site you can sign in to as an **owner**, the role that can do everything. Only owners see the import.
- Your WordPress site, still online. The import reads from it; it never changes it.
- One of these from WordPress:
  - A WordPress username with an **application password**, a separate password WordPress makes for programs. Create one in WordPress under **Users > Profile > Application Passwords**. With it, the import also reads drafts, private posts, menus and settings.
  - Or an **export file** from WordPress: **Tools > Export > All content**. The wizard accepts files up to 256 MB. Images are then downloaded from the addresses inside the file, so the old site must stay online.

## Steps

### Scan the old site

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. In the Hatamoto admin, open **Settings > Import & export > WordPress import** (`<admin>/settings/import/wordpress`).
2. Under **From the site's address**, fill in **Site address**, **WordPress username** and **Application password**. Or, under **From an export file**, choose your file.
3. Select **Scan the site**. The scan reads the old site and writes nothing to your content.
4. Read the **Scan report**. It shows:
   - How many posts, pages, drafts, images, categories, tags, authors and menus it found.
   - Your WordPress **permalink** structure, the rule WordPress uses to build post addresses, and whether Hatamoto can use the same rule.
   - Plugins it noticed, such as an SEO plugin, a page builder, a form plugin or a multilingual plugin.
   - **Expected fidelity**: the share of items that come across without loss, and each item that does not, with the reason.
   - A warning if the site is in several languages. Every language is imported and tagged with its own locale. Only the site language is public, because one install serves one language.

### Choose and run

1. Select **Choose what to import**. On **Import options**, pick the **content types**, the kinds of content such as posts and pages, to bring across, and switch **Menus**, **Drafts, private and password-protected content** and **Download images and files** on or off.
2. Under **URL structure**, choose whether to copy the WordPress permalinks. Copying them keeps most old addresses exactly the same. If your Hatamoto site already has content, this choice is not offered: its address rules stay, and every old address that moves gets a **301 redirect**, a permanent forward to the new address.
3. Select **Start the import**. The page refreshes by itself while it works. You may close it and come back; **Stop the import** keeps what is already imported, and you can resume later.

### Review the report

1. When it finishes, read the **Report**. It has these parts:
   - **Items that failed**, with the reason.
   - **Pages to review**: pages that came across with some loss, for example a page built with a page builder plugin, whose text is kept but whose layout is not. Each links to its edit screen.
   - **Redirects created**: the forwards from old addresses to new ones.
   - **URL audit**: every old address the import checked on this site, and those that do not lead to a page.
   - **wp-content addresses left in the text**: links to old WordPress files. This list should be empty.
2. Open each page under **Pages to review** and fix it in the editor.

### Pick up later changes

1. If the old site changes before you switch over, open the finished report and select **Re-sync from the old site**. A re-sync updates what it imported earlier and never makes duplicates. It overwrites pages you edited in Hatamoto since the import, unless you switch on **On a re-import, leave pages alone that I edited here since the last import**. Re-sync only before you switch your domain over.

## Check it worked

Open the **URL audit** part of the report. Every old address should be listed as passing: it answers directly, or forwards once with a 301 to a page that answers. Then visit a few old addresses on the new site in your browser and see the right page.

If the audit lists failures, open each one. A common cause is an address the import could not map, such as an old category page you removed. Add a redirect for it as shown in [Keep old links working after a move](redirects-after-migration.md).

If the scan fails to reach a site on your own network, such as a test copy at a private address, the wizard cannot read it. An administrator can run the same import from the server's command line with `--allow-private-hosts`; see the [WordPress import reference](wordpress-import-reference.md).
