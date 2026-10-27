---
title: Connect an AI agent
nav_title: Connect an AI agent
description: Give an AI assistant its own key so it can read your content and reports, then connect it and make a first request.
next: mcp.md, ../admin/users-and-api-keys.md
---

In this guide you connect an AI agent, such as an AI assistant on your computer, to your site so it can read your content and your crawler reports. You create a key for it in the admin, add your site to the agent's settings, and make a first request. At the end the agent can answer questions about your site from live data.

## Before you start

- A running Hatamoto site at a public address such as `https://example.com`, and an **owner** account, the role that can change every setting. Only owners can create keys.
- An AI tool that supports **MCP**, the Model Context Protocol, a standard way for AI tools to read and change a site. Many desktop assistants and coding tools do; check your tool's documentation for "MCP servers".
- Optional: a terminal with `curl`, to test the connection without the AI tool.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. In the admin, open **Settings > API** (`<admin>/settings/api-keys`) and select **New API key**.

2. Fill in the form:

   - **Name**: `research agent`. The name shows up in the key list and in the history of anything the agent changes.
   - **Access**: tick **Read entities** and **Read reports**. These are **scopes**, the limits of what a key may do. Read entities lets the agent read your content, menus, redirects, sitemap and `llms.txt`. Read reports lets it read the AI crawler figures.
   - **Expires**: **In 90 days**. A key that ends on its own is safer than one you forget.

   Leave **Write entities** off for now. With it, an agent can create, publish and unpublish pages with no person checking first.

3. Select **Create key**. The list opens with a banner, **Copy your API key**, and the note that this is the only time the key is shown. Copy it now and keep it somewhere safe. A key starts with `nxk_`.

   An **API key** is a secret string a program uses to act on your site. This one acts on your behalf: it can never do more than your own account, and it stops working if your account loses its owner or editor role.

4. Open your AI tool's MCP settings and add a server. Most tools accept a block like this one. Replace `<your-key>` with the key you copied:

   ```json
   {
     "mcpServers": {
       "hatamoto": {
         "url": "https://example.com/mcp",
         "headers": { "Authorization": "Bearer <your-key>" }
       }
     }
   }
   ```

   The address is your site followed by `/mcp`. The key travels in the `Authorization` header of every request.

5. Restart or reload the AI tool. It connects and loads the list of tools your site offers, such as `entities_list`, `search` and `analytics_crawler_hits`.

6. Ask the agent: "Using the hatamoto tools, list my five newest posts and tell me which AI crawlers fetched my site this month." It calls `entities_list` and `analytics_crawler_hits` and answers from live data.

7. Optional, to test without the AI tool: send the opening message of the protocol yourself. In a terminal, store the key in a variable, then run the request:

   ```bash
   HATAMOTO_KEY=<your-key>
   curl -s -X POST https://example.com/mcp \
     -H "Authorization: Bearer $HATAMOTO_KEY" \
     -H "Content-Type: application/json" \
     -H "Accept: application/json, text/event-stream" \
     -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
   ```

   The answer names the server:

   ```text
   event: message
   data: {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"logging":{},"tools":{"listChanged":true}},"instructions":"Hatamoto CMS. Create drafts with entities_create, review them, then entities_publish. Read schemas_get first to learn which attributes a content type takes.","protocolVersion":"2025-06-18","serverInfo":{"name":"hatamoto","version":"1"}}}
   ```

## Check it worked

Back in **Settings > API**, the **Last used** column of `research agent` shows today's date, and its status is **Active**.

If the agent or `curl` gets `401`, the key is missing, mistyped, revoked or expired. Copy it again from where you saved it, or create a new one. A key that is lost cannot be shown again; select **New secret** on its row to get a new one (the old one stops working at once).

If the agent reports `forbidden: the API key lacks the read:reports scope`, the key is missing that scope. The admin cannot change a key's access after it is made, so create a new key with the right boxes ticked and revoke the old one.

If the agent runs in a web page and gets `403`, the request came from a browser address your site does not trust. A developer can allow it with `HATAMOTO_MCP_ALLOWED_ORIGINS`; see [MCP server](mcp.md#origin-check).

Every request the agent makes is written to an audit log, and anything it changes appears in the content's history tagged with `[mcp:research agent]`. When you later want the agent to draft content, give it a separate key with **Write entities**, and read [MCP server](mcp.md#good-practice) first.
