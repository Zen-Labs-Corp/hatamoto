---
title: Keep old links working after a move
nav_title: Keep old links working
description: Forward old addresses to their new pages after you move your site, so visitors and search engines never land on a missing page.
next: redirect-rules.md, ../ai-seo/check-sitemap-and-structured-data.md
---

After you move a site, other websites and search engines still link to its old addresses. In this guide you forward each old address that changed to its new page, retire a whole group of old addresses at once, and watch for the ones you missed.

## Before you start

- A Hatamoto site you can sign in to as an owner or editor.
- Your content already moved in, with [Import from WordPress](wordpress-import.md) or [Export and import](export-import.md).
- A list of old addresses that matter: the import's URL audit, your old sitemap, or your most visited pages.

A **redirect** forwards a visitor from one address to another. Hatamoto uses a **301 redirect**, a permanent forward, unless you choose otherwise. Search engines move a page's ranking to the new address when they see a 301. A redirect wins over a page at the same address, so a rule you write always takes effect.

## Steps

### Forward one old address

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. In the admin, open **Settings > Redirects** (`<admin>/settings/redirects`).
2. Select **New redirect**.
3. In **From**, type the old address without your domain and without a slash at the end, for example `/our-services`. A slash at the end of a visitor's address is ignored, so `/our-services` also catches `/our-services/`.
4. In **To**, type the new address, for example `/services`. A full address such as `https://example.com/services` also works.
5. Leave **Type** at **301: moved permanently**.
6. Select **Create redirect**. The rule works at once.

### Retire a whole group of addresses

Sometimes a whole part of the old site moved, such as everything under `/old-blog`. A **wildcard rule** forwards a whole group of addresses at once: `*` stands for one part of the address between slashes, and `**` at the end stands for everything that follows.

1. Create two redirects, both with type 301, as in the steps above:

   | From | To |
   |---|---|
   | `/old-blog` | `/blog` |
   | `/old-blog/**` | `/blog/$1` |

   `$1` is replaced with what the wildcard matched, so `/old-blog/hello` goes to `/blog/hello`. To send the whole group to one page instead, use that page as the target without `$1`, for example `/blog`.

2. Open the **Explain a URL** tab (`<admin>/settings/redirects/explain`), type an old address such as `/old-blog/hello` in **Address path** and select **Explain**. It shows which rule matched and where the visitor ends up.

### Catch what you missed

1. After you switch your domain over, open the **404 log** tab (`<admin>/settings/redirects/404`) every few days. It lists addresses visitors asked for that do not exist, with how often and where they came from.
2. For each address that should lead somewhere, select **Create redirect** on its row. The **From** field is filled in for you; add the **To** address and save.
3. For addresses that should stay missing, such as old login pages that robots try, select **Ignore**.

### For a site that came from WordPress

1. Old WordPress sites often have dated post addresses such as `/2024/05/hello`, feed addresses such as `/feed`, and `/page/2` style pages. Hatamoto can forward these families for you with the **legacy WordPress redirects** setting. It is off by default and has no switch in the admin: an owner turns it on with one command, shown on the [Redirect rules](redirect-rules.md) page together with the exact list of what it forwards.

## Check it worked

Open an old address in a private browser window, for example `https://example.com/our-services`. The browser lands on the new page, and the address bar shows the new address. In the redirect list, the rule's **Hits** count goes up after a short while.

If the old address still shows a "page not found" error, open **Explain a URL** and test it. The most common causes:

- The **From** address has a typing mistake or a different case: `/Our-Services` and `/our-services` are different addresses.
- The rule is turned off: its **State** in the list is **Off**. Select **Turn on** on its row.
- You created it with the `hatamoto redirect create` command on the server. The running site reloads its rules every 60 seconds, so wait a minute and try again.

If the admin refuses a wildcard rule as unsafe, the first part of the address must be fixed text (not `*`), and the target must not match its own source, which would make a loop. All the rules and messages are in [Redirect rules](redirect-rules.md).
