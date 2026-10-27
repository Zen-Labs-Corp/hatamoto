---
title: Set the addresses of your pages
nav_title: Set page addresses
description: Choose where a kind of content lives on your site, publish an entry there, and change an address later without breaking old links.
next: check-sitemap-and-structured-data.md, url-patterns.md
---

In this guide you create a new kind of content, guides, whose entries live at addresses such as `/guides/install-the-app`, with a listing page at `/guides`. Then you change one address and see the old one forward to the new one. At the end you know how Hatamoto builds every address on your site.

## Before you start

- A running Hatamoto site you can sign in to as an **owner**, the role that can change every setting.
- A plan for the address you want. Changing it later is safe, but old links then depend on forwards.

Two terms help. A **content type** is a kind of content, such as a page or a post, which decides its fields and its address. A **slug** is the short name in an address, such as `install-the-app` in `/guides/install-the-app`. Each content type has a **URL pattern**, the rule that builds the address from the slug.

The built-in page and post types keep their fixed addresses (`/<slug>` for pages and `/blog/<slug>` for posts), so you set an address on a content type of your own.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. In the admin, open **Settings > Entity types** (`<admin>/settings/types`) and select **New entity type**.

2. In **Basics**, set **Name** to `Guide`. The **Slug** fills in as `guide`. It identifies the type and cannot be changed later.

3. In **Fields**, choose **Rich text** under **Add a field** and select **Add field**. Set its **Label** to `Content`. This is the box your editors write in.

4. In **Addresses and search**, set **Address pattern** to:

   ```text
   /guides/{slug}
   ```

   Below the field, an example appears as you type:

   ```text
   Example: /guides/example-page
   ```

   `{slug}` is replaced by each entry's slug. The pattern must start with `/` and end with `{slug}` (or `{path}`, for pages nested under other pages).

5. Set **Archive address** to `/guides` and **Archive title** to `Guides`. The archive is the page that lists every guide, and the section name in each guide's breadcrumb.

6. Select **Save**.

7. Open **Entities**, select **Create**, then **Guide**.

8. Set **Name** to `Install the app`. Leave **Slug** empty: Hatamoto makes `install-the-app` from the name.

9. Write a line in **Content**, set **Status** to **Published**, and select **Save**.

10. Open `https://example.com/guides/install-the-app`. Your guide is there. `https://example.com/guides` lists it.

11. Open the guide again, change **Slug** to `install`, and save.

12. Open the old address, `https://example.com/guides/install-the-app`. Your browser lands on `/guides/install`. Hatamoto added a **301 redirect**, a permanent forward from the old address to the new one, so links and search results that use the old address keep working. You find it in **Settings > Redirects**.

   The same happens for a whole content type: when you change its address pattern, every published entry that moves gets a forward from its old address. A redirect you wrote yourself is never replaced.

## Check it worked

Run this from a terminal to see the forward:

```bash
curl -sI https://example.com/guides/install-the-app | grep -iE '^(HTTP|location)'
```

```text
HTTP/2 301
location: /guides/install
```

The first line may read `HTTP/1.1 301 Moved Permanently`, depending on the web server in front of Hatamoto.

Then open `https://example.com/sitemap.xml`. It now lists a `sitemap-guides.xml` file, because each content type's first address segment, `guides`, becomes its group in the sitemap. That file lists `/guides` and `/guides/install`, and not the old address.

If saving the content type shows an error under **Address pattern**, the pattern breaks a rule: it must start with `/`, end with `{slug}` or `{path}`, and use each token once as a whole segment (`/a-{slug}` is not allowed). The reason is shown under the field.

If the new address answers "not found", check that the guide's **Status** is **Published**. Drafts have no public address.

Addresses can also hold dates (`/news/{year}/{month}/{slug}`), nested pages (`/{path}`) and more. Every token and setting is in [URL patterns](url-patterns.md). If you are moving a whole site, load the old addresses as redirects first; see [Redirects after migration](../migrate-contribute/redirects-after-migration.md).
