---
title: Let an AI or a script edit your site safely
nav_title: Edit with AI safely
description: Give an assistant or script a limited key, hold its changes for review where you can, and preview, check and undo any change.
next: mcp.md, ../admin/users-and-api-keys.md, connect-an-ai-agent.md
---

In this guide you let an AI assistant or a script change your content without losing control of it. You choose what its key may do, decide which of its changes wait for a person, and learn to preview, apply and undo a change. At the end you know what the audit log shows about every change it made.

## Before you start

- An **owner** account and a running site, with an **API key** for the assistant or script. An API key is a secret string a program uses to act on your site. To make one, follow [Connect an AI agent](connect-an-ai-agent.md).
- The assistant talks to your site over **MCP**, the Model Context Protocol, a standard way for AI tools to read and change a site. A script can use the REST API instead; both are covered here.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

### Choose the key's access

1. Tick only the **scopes** the key needs. A scope is a limit on what a key may do. **Read entities** lets it read content and its history. **Write entities** also lets it edit, publish, unpublish and restore.
2. Remember the key acts as the person who made it. It never does more than that person's **role**, which is what an account may do, and it stops working if that person loses the role. Tools and scopes are listed in [MCP server](mcp.md).

### Keep its changes from going live on their own

3. Review is off by default. With it off, a key that has **Write entities** publishes and edits live content with no human step.
4. To hold changes, switch Review on at `<admin>/settings/review`. A key follows the role of its creator. If that role waits (Editor, Author or Contributor), a change to published content is held: the REST API answers `202` and MCP returns `status: pending_review` with a `changeset_id`, and nothing is stored until a reviewer approves it.
5. An Owner's key publishes directly even with Review on. Keys made in the admin belong to the owner who made them, so for a reviewed key create it on the server as an Editor:

   ```bash
   hatamoto apikey create --name "docs agent" --scopes read:entities,write:entities --as editor@example.com --out ./agent.key
   ```

6. A key never approves a change, not even its own. New entries made by a key are always drafts. A build can also register its own publishing policy.

### Read, preview, apply and undo

7. Read the entry first. Its `version` counts changes to its attributes. Over REST it is the `ETag` header; over MCP, `entities_get` returns it.
8. Preview before you write. Add `?dry_run=1` over REST, or `dry_run: true` in `entities_update`. Nothing is stored. The answer lists `changes` with `path`, `before` and `after`, the `version` it was built from, any `errors`, and `pending_review: true` if the real change would be held.
9. Apply the change with that `version` (REST: `If-Match`). If someone changed the entry meanwhile, the write is refused (`412` over REST, `conflict: stale version` over MCP) and nothing is stored. Read again and retry.
10. By default `attributes` replace the stored ones. To change only some keys, use `merge: true` over MCP, or `Content-Type: application/merge-patch+json` with PATCH over REST. Keys you send change, `null` removes one.
11. To undo, list the earlier versions and restore one. A restore stores a new version, so history is kept. Over MCP use `entities_versions`, `entities_version_get` and `entities_restore`; over REST use `POST /api/v1/entities/{id}/restore/{version}`. A restore of published content is held for review like any edit.

### See what it did

12. Open the Audit log at `<admin>/audit`, which owners can read. A REST change shows as Source **API**. An MCP call shows as Source **Agent**, refused calls too, and the change itself also appears with the key's name. A dry run stores nothing, so it adds no entry for the content itself.
13. The entry's version history tags each change `[api-key:<name>]` or `[mcp:<name>]`.

### Example: cancel an event

Read the event with `entities_get`. It returns `version` 7:

```json
{"id": "<id>"}
```

Preview the change with a merge, so other attributes stay as they are:

```json
{"id": "<id>", "attributes": {"notice": "Cancelled"}, "merge": true, "version": 7, "dry_run": true}
```

The answer shows what would change:

```json
{"dry_run": true, "version": 7, "changes": [{"path": "notice", "before": null, "after": "Cancelled"}], "errors": []}
```

Send the same call without `dry_run`. The entry is now at version 8. To undo it, restore version 7, checking that nobody edited in between:

```json
{"id": "<id>", "version": 7, "expected_version": 8}
```

Over REST the same preview is one request:

```bash
curl -X PATCH -H "X-API-Key: <your-key>" -H "Content-Type: application/merge-patch+json" -H 'If-Match: "7"' -d '{"attributes": {"notice": "Cancelled"}}' "https://example.com/api/v1/entities/<id>?dry_run=1"
```

## Check it worked

In the Audit log you see the change under the key's name, as Source **API** or **Agent**. With Review on and a key made as an Editor, a change to a published entry waits in the Review queue (`<admin>/review`) until you approve it.

If you see `conflict: stale version`, the entry changed since you read it. Read it again, preview again and retry. If an owner's key published something you wanted held, replace it with a key made with `--as` an Editor.
