---
title: Let an AI or a script edit your site safely
nav_title: Edit with AI safely
description: Give an assistant or script a limited key, hold its changes for review where you can, and preview, check and undo any change.
next: mcp.md, ../admin/users-and-api-keys.md, connect-an-ai-agent.md
---

In this guide you let an AI assistant or a script change your content without losing control of it. You choose what its key may do, decide which changes wait for a person, and learn to preview, apply and undo a change.

## Before you start

- An **owner** account and a running site. Your assistant talks to it over **MCP**, the Model Context Protocol, a standard way for AI tools to read and change a site. A script can use the REST API instead. [Connect an AI agent](connect-an-ai-agent.md) shows how to hand over the key.
- A shell on the server, with the service's settings loaded so `DATABASE_URL` (where its database is) is set. The key command talks to the database directly.
- An **Editor** account for the key to act as. See [Users and API keys](../admin/users-and-api-keys.md).

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

### Make a limited key

1. An **API key** is a secret string a program uses to act on your site. It acts as the account it is made for, so it never does more than that account's current **role**, and stops working if the account is removed. Make it on the server for your Editor account (`editor@example.com` is a placeholder), with only the **scopes**, the limits on what a key may do, it needs: `read:entities` reads content and history, `write:entities` also edits, publishes, unpublishes and restores:

   ```bash
   hatamoto apikey create --name "docs agent" --scopes read:entities,write:entities --as editor@example.com --out ./agent.key
   ```

2. Do not keep using a key made in the admin: it belongs to you, the owner, and skips Review. Revoke it, or never give it **Write entities**. [MCP server](mcp.md) lists the tools and scopes.

### Hold its changes for review

3. Review is off by default. With it off, a key that has **Write entities** publishes and edits live content with no human step.
4. Switch Review on at `<admin>/settings/review`. A key follows its creator's role. Owner and Reviewer keys publish directly even with Review on. Editor, Author and Contributor keys wait: a change to published content, publishing a draft and scheduling are held. REST answers `202`, MCP returns `status: pending_review` with a `changeset_id`, and nothing is stored until a reviewer approves it.
5. Review does not hold everything. Taking a page offline (unpublish) is never held, so a key with **Write entities** can still do it at once. A person entry made public goes live at once.
6. A key never approves a change, not even its own. New pages and posts made by a key are drafts.

### Preview, apply and undo

7. Read the entry first. Its `version` counts changes to its attributes: the `ETag` header over REST, `entity.version` from `entities_get` over MCP.
8. Preview before you write: `?dry_run=1` over REST, `dry_run: true` in `entities_update`. Nothing is stored. The answer lists `changes` (`path`, `before`, `after`), the `version`, any `errors`, and `pending_review: true` if the real change would be held.
9. Apply the change with that `version` (REST: `If-Match`). If the entry changed meanwhile, the write is refused (`412` over REST, `conflict: stale version` over MCP).
10. `attributes` replace the stored ones by default. To change only some keys, use `merge: true` over MCP, or PATCH with `Content-Type: application/merge-patch+json` over REST. `null` removes a key.
11. To undo, restore an earlier version: it is stored as a new version, so history is kept. Use `entities_versions`, `entities_version_get` and `entities_restore` over MCP, or `POST /api/v1/entities/{id}/restore/{version}` over REST. Restoring published content is held.

### See what it did

12. Owners read the Audit log at `<admin>/audit`. A REST change shows as Source **API**. An MCP write adds two rows under the key's name: the tool call as Source **Agent** (refused calls too) and the change itself as Source **API**.
13. Version history tags content changes `[api-key:<name>]` (REST) or `[mcp:<name>]` (MCP). A REST restore is untagged, and publish and unpublish write no version, so use the Audit log for those.

### Example: cancel a live event

This assumes Review on and the Editor key from step 1, so the change is held. Blocks are MCP tool input; `<id>` is the event's id, and its type needs a `notice` field. Call `entities_get`, which returns the entry with `version` 7:

```json
{"id": "<id>"}
```

Preview with `entities_update`:

```json
{"id": "<id>", "attributes": {"notice": "Cancelled"}, "merge": true, "version": 7, "dry_run": true}
```

```json
{"dry_run": true, "version": 7, "changes": [{"path": "notice", "before": null, "after": "Cancelled"}], "errors": [], "pending_review": true}
```

Send the same call without `dry_run`. It returns `status: pending_review` and the event stays at version 7 until you approve it in `<admin>/review`; it is then at version 8. To undo, call `entities_restore`, which is held again until approved:

```json
{"id": "<id>", "version": 7, "expected_version": 8}
```

Over REST, with `<your-key>` the key's secret:

```bash
curl -X PATCH -H "X-API-Key: <your-key>" -H "Content-Type: application/merge-patch+json" -H 'If-Match: "7"' -d '{"attributes": {"notice": "Cancelled"}}' "https://example.com/api/v1/entities/<id>?dry_run=1"
```

## Check it worked

The Audit log shows the change under the key's name, and a change to a published entry waits in `<admin>/review` until you approve it.

If you see `conflict: stale version`, read, preview and retry. If something went live that you wanted held, the key is an Owner or Reviewer key: replace it with one made as in step 1.
