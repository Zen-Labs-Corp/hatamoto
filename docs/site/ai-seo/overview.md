---
title: Tour what search engines and AI tools see
nav_title: Overview
description: Look at the files and screens that decide what search engines and AI tools get from your site, and learn where each one is controlled.
next: control-ai-crawlers.md, see-ai-crawler-visits.md, connect-an-ai-agent.md
---

In this guide you look at the files and screens that decide what search engines and AI tools see on your site. At the end you know where each one lives and which guide to follow to change it. All of it is built in: there is nothing to install.

## Before you start

- A running Hatamoto site you can sign in to as an owner.
- Its public address, such as `https://example.com`.
- A web browser. A terminal with `curl` helps, but is not required.

## Steps

1. Check the public address. Open `https://example.com/robots.txt` and read the last line:

   ```text
   Sitemap: https://example.com/sitemap.xml
   ```

   Every address that machines get from your site starts with this one. If it shows `localhost` or an address you do not recognise, ask whoever runs the server to set `PUBLIC_BASE_URL`, an **environment variable** (a named setting a program reads when it starts) that holds your public address. The [URL patterns](url-patterns.md#the-public-base-url) page explains how the address is chosen.

2. Read the rest of `robots.txt`. **`robots.txt`** is a file at the root of your site that tells **crawlers**, programs that fetch pages automatically for a search engine or an AI company, what they may fetch. By default it lets them read everything except a few internal paths:

   ```text
   User-agent: *
   Allow: /
   Disallow: /auth
   Disallow: /api
   Disallow: /client
   Disallow: /*?*_rsc=
   ```

   On an install that still serves the admin at the old address `/admin`, the file also lists `Disallow: /admin`. On any other address it lists nothing about the admin, so the file does not give your admin address away.

   You can tell groups of AI crawlers to stay away. [Control AI crawlers](control-ai-crawlers.md) shows how.

3. Open `https://example.com/sitemap.xml`. A **sitemap** is a file that lists your pages for search engines. This one is an index that points to one file per group, such as `sitemap-blog.xml` for posts. Your browser shows it as a readable page. Hatamoto rebuilds it from your published content, so you never edit it.

4. Open `https://example.com/llms.txt`. **`llms.txt`** is a short index of your published content written for AI tools. It names your site, counts your published content and links to a feed of all of it. Like the sitemap, it follows what you publish.

5. Look at the structured data of one page. Open a published page, view its source (in most browsers, press Ctrl+U or Cmd+Option+U), and search for `application/ld+json`. That block is **JSON-LD**, machine-readable data that tells search engines what the page is about, in the shared schema.org vocabulary. A post is described as an `Article` with its headline, author and dates. [Check your sitemap and structured data](check-sitemap-and-structured-data.md) shows how to test it.

6. Sign in to the admin and open **AI analytics** in the sidebar. The **Crawlers** tab shows which AI crawlers fetched your pages, and whether each visit really came from the company it claims. The **Referrals** tab counts people who reached you by clicking a link inside an AI assistant such as ChatGPT. [See AI crawler visits](see-ai-crawler-visits.md) walks through each view.

7. Open a post in the editor and choose the **SEO and AEO** tab. The **Include in /llms.txt** switch (in the **Advanced** tab of the panel) removes this post from `llms.txt` and from the feed it links to. The same tab has **Ask search engines not to list this post**, which keeps it out of the sitemap and site search. Pages have the same no-index switch in their **Search and social** card.

8. Note the address `https://example.com/mcp`. It is where an AI agent connects through **MCP**, the Model Context Protocol, a standard way for AI tools to read and change your site. An agent needs an API key you create. [Connect an AI agent](connect-an-ai-agent.md) covers it.

9. Before you launch, run through this list once:

   - The `Sitemap:` line in `robots.txt` shows your real address.
   - `/robots.txt`, `/sitemap.xml` and `/llms.txt` open and look right.
   - You decided which kinds of AI crawler, if any, you want to turn away.
   - The address of each kind of content is the one you want. Changing it later is possible, but old links then rely on redirects. See [Set the addresses of your pages](set-page-addresses.md).
   - If you move from another system, your old addresses are loaded as redirects first. See [Redirects after migration](../migrate-contribute/redirects-after-migration.md).

## Check it worked

You can now say where each part lives:

| What | Where |
|---|---|
| Rules for crawlers | `/robots.txt`, set in **AI analytics > Crawlers > Robots policy** |
| List of pages for search engines | `/sitemap.xml` and its child files |
| Index for AI tools | `/llms.txt` |
| Structured data | A `application/ld+json` block in each page's source |
| Crawler visits and AI referrals | **AI analytics** in the admin |
| Per-page switches | The editor's search and SEO cards |
| Connection for AI agents | `/mcp`, with an API key |

If `/robots.txt`, `/sitemap.xml` or `/llms.txt` shows an address such as `http://127.0.0.1:8090`, the server is not told its public address. Ask whoever runs it to set `PUBLIC_BASE_URL` and restart Hatamoto, then reload the file.
