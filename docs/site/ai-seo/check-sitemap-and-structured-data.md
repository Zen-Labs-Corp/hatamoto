---
title: Check your sitemap and structured data
nav_title: Check sitemap and data
description: Make sure search engines get the right list of pages and a correct description of each one, before launch and after changing addresses.
next: sitemaps.md, json-ld.md
---

In this guide you check the two things search engines read besides your text: the list of your pages and the description of each page. You also keep a copy of the list, so you can compare it after you change addresses. Run it before launch and after any change to how addresses are built.

## Before you start

- A running, published Hatamoto site at its real address, such as `https://example.com`.
- A terminal with `curl` for the copy-and-compare steps. A browser is enough for the rest.
- Optional: an AI agent connected to your site, as in [Connect an AI agent](connect-an-ai-agent.md).

Two terms. A **sitemap** is a file that lists your pages for search engines. **JSON-LD** is a block of machine-readable data in each page that tells search engines what the page is about, in the shared **schema.org** vocabulary.

## Steps

1. Open `https://example.com/sitemap.xml`. It is an index of smaller files, one per group of content, each with the date of its newest change. A site with pages and posts shows two:

   ```text
   https://example.com/sitemap-blog.xml    2026-01-14
   https://example.com/sitemap-misc.xml    2026-01-10
   ```

   `misc` holds your home page, your pages and anything without a group of its own. It is always last.

2. Open one of the files, such as `sitemap-blog.xml`. It lists the blog listing page first, then every published post, newest change first. To count the entries from a terminal:

   ```bash
   curl -s https://example.com/sitemap-blog.xml | grep -c '<url>'
   ```

3. Look for a page you expect. A page is left out when it is not published, when **Ask search engines not to list this page** is switched on, when a redirect sends its address elsewhere, or when its whole content type is set to **Leave this type out of the sitemap**.

4. Save a copy of every address in the sitemap, before you change addresses:

   ```bash
   for f in $(curl -s https://example.com/sitemap.xml | grep -o 'https://[^<]*sitemap-[^<]*\.xml'); do
     curl -s "$f" | grep -o '<loc>[^<]*</loc>'
   done | sort > before.txt
   ```

   After the change, run the same loop into `after.txt` and compare the two:

   ```bash
   diff before.txt after.txt
   ```

   Every address that left the list should forward to its new place. Hatamoto adds those forwards itself when you rename a page or change a content type's address pattern; check one by opening the old address.

5. Submit `https://example.com/sitemap.xml` once in each search engine's webmaster tool, such as Google Search Console or Bing Webmaster Tools. Your `robots.txt` already points to it, so this only speeds up the first visit.

6. Open a published post and view its source. Search for `application/ld+json`. The first block describes the post: an `Article` with its `headline`, `author`, `datePublished` and `url`. Another block describes your organisation and website.

7. Ask for the data alone. Replace `/blog/hello` with the address of one of your posts:

   ```bash
   curl -s -H 'Accept: application/ld+json' https://example.com/blog/hello
   ```

   The answer is the JSON-LD of that page instead of the HTML.

8. Check that three addresses agree for the same page: the `url` in the JSON-LD, the canonical link in the page head (`rel="canonical"`) and the entry in the sitemap.

9. Paste the page address into Google's Rich Results Test to see which search features it qualifies for.

10. Optional: ask your connected AI agent to "run `jsonld_validate` for this post" and "run `sitemap_preview`". The first lists missing fields per type as errors and warnings. The second returns the sitemap exactly as a crawler gets it, with the number of entries.

## Check it worked

The sitemap lists every page you expect, the comparison shows only the changes you meant, every moved address forwards, and the JSON-LD of a post shows its real headline, author, date and address.

If the addresses in the sitemap or the JSON-LD start with `http://localhost` or an internal address, the server is not told its public address. Ask whoever runs it to set `PUBLIC_BASE_URL` and restart Hatamoto.

If a page you unpublished still appears, the file may be cached for up to an hour. Check that the change was saved, then try again later.

If the JSON-LD of a page appears twice, your theme or a plugin writes its own as well. A developer can remove the duplicate; see [JSON-LD](json-ld.md#extending-it-with-plugins). Every rule for what goes in the sitemap is in [Sitemaps](sitemaps.md).
