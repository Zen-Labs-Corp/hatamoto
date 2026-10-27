---
title: See which AI crawlers visit your site
nav_title: See crawler visits
description: Find out which AI crawlers read your pages, which pages they skip, and how many people reach you from an AI assistant.
next: access-log-ingest.md, crawler-analytics.md
---

In this guide you read the AI analytics in the admin: which AI crawlers fetched your pages, which pages none of them has read yet, and how many people clicked through to you from an AI assistant. At the end you can answer "who reads my site, and does it send visitors back?"

## Before you start

- A running Hatamoto site you can sign in to. Any admin role can read these screens.
- Some days of traffic. A new site shows empty charts until crawlers find it.
- Optional: a terminal with `curl`, to make a test visit.

A **crawler** is a program that fetches web pages automatically, for a search engine or an AI company. Hatamoto records every visit from a crawler it knows by name, on its own, with no plugin and no cookie.

## Steps

1. In the admin, open **AI analytics** in the sidebar. The **Crawlers** tab opens on **Overview**. The range at the top is the last 30 days; you can pick any range up to 366 days.

2. Read the figures across the top:

   | Figure | Meaning |
   |---|---|
   | AI fetches | Pages and files AI crawlers fetched. |
   | Verified | How many of those fetches came from the company the crawler claims to belong to. |
   | Spoofed | Fetches that claimed a crawler's name but came from somewhere else. |
   | Visits from AI assistants | People who clicked a link to you inside an AI assistant. |
   | Pageviews | All page loads by people. |

   Below them, the chart shows fetches over time. Switch the grouping between operator (the company, such as OpenAI) and purpose (training, search and so on).

3. Open the **Agents** view. Each row is one crawler, such as GPTBot or ClaudeBot, with its company, its purpose and its number of fetches. Select a name to see its activity, the pages it fetches most and the answers it got. A note tells you when this crawler may ignore your `robots.txt`.

4. Open **Pages**. The first lists show the pages AI crawlers fetch most. The last list, **Published pages no AI agent has fetched**, shows pages no AI crawler has read yet. Those are good candidates to link from pages crawlers already read.

5. Open **First crawls**. Each row is a page that an AI crawler fetched for the first time in the range, which crawler it was, and how long after publishing that happened.

6. Open **Errors**. It lists fetches that failed, grouped by page and status, such as `404` (not found). If a crawler keeps asking for an old address, select **Create redirect** on its row to forward it to the right page.

7. Open **Spoofed**. Anyone can pretend to be GPTBot. Hatamoto checks every visit in the background against the addresses each company publishes, and lists here the visits that failed the check. A new visit is unchecked for a few minutes until that check runs.

8. Open the **Referrals** tab at the top. It counts, per day and per assistant, the people who clicked through to you from ChatGPT, Perplexity, Claude, Gemini, Copilot, Grok, Meta AI, DeepSeek or Mistral. **AI share of traffic** compares those visits with all pageviews. Only the assistant and the day are stored: no visitor, no address, no cookie.

9. Open a published post in the editor and choose the **SEO and AEO** tab. The **AI crawlers** card shows how often assistants fetched this post in the last 7 and 30 days, when it was first crawled, and the last visit of each AI crawler.

## Check it worked

Make a test visit that looks like OpenAI's crawler. Run this on your own computer:

```bash
curl -s -o /dev/null -A GPTBot https://example.com/
```

Wait a few seconds, then reload **AI analytics > Crawlers > Agents**. GPTBot now has at least one fetch, and the home page appears under **Pages**. A few minutes later the visit shows under **Spoofed**, because it came from your computer and not from OpenAI. That is the check working as intended.

If nothing appears:

- Check the date range includes today.
- Check that the address you fetched is a page, `robots.txt`, a sitemap, `llms.txt` or a feed. Images and other files under `/media/` are not recorded this way. To see crawlers fetching them, a developer can feed your web server's access log into Hatamoto with [Access-log ingest](access-log-ingest.md).

If real crawlers such as Googlebot all show as spoofed, your site probably sits behind a CDN that hides the visitor's address. The fix is described in [Access-log ingest](access-log-ingest.md#the-client-ip-caveat). For how long data is kept and every report field, see [Crawler analytics](crawler-analytics.md).
