---
title: Control what AI crawlers may use
nav_title: Control AI crawlers
description: Tell AI training crawlers and other kinds of bots to stay away, and keep single posts out of the index AI tools read.
next: see-ai-crawler-visits.md, robots-llms.md
---

In this guide you tell one kind of AI crawler, those that collect pages to train AI models, to stay away from your site. You also keep one post out of the index that AI tools read. Both changes take a few clicks in the admin and need no file editing.

## Before you start

- A running Hatamoto site you can sign in to as an **owner**, the role that can change every setting. Editors can see the policy but not change it.
- At least one published post, for the second part.
- Optional: a terminal with `curl`, to check the result.

A **crawler** is a program that fetches web pages automatically, for a search engine or an AI company. Hatamoto knows 43 of them by name and sorts them into eight kinds, called purposes.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. In the admin, open **AI analytics** in the sidebar, then the **Crawlers** tab, then **Robots policy** (`<admin>/analytics/crawlers?tab=robots`).

2. Read the list. Each row is one kind of crawler, in this order:

   | Row | What those crawlers do |
   |---|---|
   | Training | Collect pages to train AI models. |
   | Search index | Build the search index of an AI assistant. |
   | User fetch | Fetch one page because a person asked an assistant about it. |
   | Other AI | Other AI-related fetching. |
   | Search | Classic search engines such as Google and Bing. |
   | Social | Link previews in chat and social apps. |
   | SEO | Search marketing tools. |
   | Archive | Web archives. |

   Under each row, "robots.txt names" lists the crawler names the row controls. "May ignore robots.txt" names crawlers known not to obey it.

3. Switch off **Allow Training**. Leave the other rows on. Every row starts switched on: nothing is turned away until you choose.

4. Select **Save robots policy**. The admin confirms with "Robots policy saved. robots.txt now tells these crawlers to stay away."

   Hatamoto now adds a short block to your **`robots.txt`**, the file at the root of your site that tells crawlers what they may fetch. Each training crawler gets its own block that asks it to stay away from the whole site:

   ```text
   User-agent: GPTBot
   Disallow: /
   ```

   Rows you leave on add nothing, so your normal rules stay as they are.

5. Open **Entities**, then the post you want to keep out, so it opens in the post editor.

6. Choose the **SEO and AEO** tab (AEO, answer engine optimisation, means being found by AI tools) and switch off **Include in /llms.txt**. **`llms.txt`** is a short index of your content written for AI tools; this switch removes the post from it and from the feed it links to.

7. Select **Update** (or **Save draft** for a post that is not published yet).

   The switch is in the post editor only. For pages and other content, a developer can set the same flag through the API; see [robots.txt and llms.txt](robots-llms.md#per-page-controls).

8. If you also want a page left out of search results, open it and switch on **Ask search engines not to list this page** in the **Search and social** card (for a post, **Ask search engines not to list this post** in **Address and indexing**). Save. The page leaves the sitemap and site search, and tells search engines not to list it.

## Check it worked

Open `https://example.com/robots.txt`. Near the end, before the `Sitemap:` line, you see one block per training crawler, such as `GPTBot`, `ClaudeBot` and `CCBot`. From a terminal:

```bash
curl -s https://example.com/robots.txt | grep -A1 GPTBot
```

```text
User-agent: GPTBot
Disallow: /
```

The file may be cached for up to five minutes. If you still see the old version, wait and reload.

Open `https://example.com/llms.txt`. The number of published entities in the summary line under the title is one lower than before.

If the screen says "Only the site owner can change the robots policy.", you are signed in as an editor. Ask an owner to make the change.

Keep in mind that `robots.txt` is a request, not a lock. Well-behaved crawlers obey it; others do not. The crawlers listed under "May ignore robots.txt" mostly fetch a page because a person asked an assistant about it, so turning them away does not stop them. To see who still visits, follow [See AI crawler visits](see-ai-crawler-visits.md).
