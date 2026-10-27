# Entity import

`entity.Service.Import` is the one way bulk content enters Hatamoto: the
WordPress importer (`v1-22`), the archive import and the MCP/CLI tools call it
instead of `CreateEntity` per row.

```go
res, err := svc.Import(ctx, actor, entity.ImportBatch{
    Source: "wordpress",
    Items: []entity.ImportItem{{
        Key:         "wp:post:123",        // idempotency key, required
        SchemaSlug:  "post",
        Name:        "Hello World",
        Slug:        "hello-world",         // generated from Name when empty
        Status:      entity.StatusPublished, // draft when empty
        Attributes:  attrs,                 // validated against the schema, sanitised
        ParentKey:   "wp:page:7",           // or ParentID; a row of this or an earlier batch
        AuthorName:  "Jane Guest",          // or AuthorID; found or created by slug
        CreatedAt:   created, UpdatedAt: updated, PublishedAt: published,
    }},
})
```

* **Dates are kept.** `created_at`, `updated_at` and `published_at` are stored as
  given (UTC); missing ones default to now, to `created_at`, and (for a
  published or archived row) to `created_at`. The `updated_at` trigger is
  switched off for the import transaction only.
* **Validated and sanitised like a save**: JSON-Schema validation, the rich-text
  and HTML-section sanitisers, the `entity.before_save` plugin hook (operation
  `create`, or `update` for a re-run). An item that fails is reported in
  `ImportResult.Items[i].Err` and the rest of the batch goes on; the call itself
  errors only for an empty batch, more than `MaxImportBatch` (1000) items, or a
  repository without import support.
* **Idempotent.** `Key` is stored in `metadata.import_key` (unique). A key seen
  before updates that entity in place (same id, a version snapshot when the
  attributes changed), so a re-run, or a re-sync before cutover, never
  duplicates. `ImportResult` counts `Created`, `Updated` and `Failed`.
* **Parents** resolve by key inside the batch in any order (parents are stored
  first; a cycle, an unknown key or a parent chain deeper than 8 fails the item).
  A slug that is already used by a sibling fails the item.
* **Authors**: `AuthorID` must exist; `AuthorName` (or a plain `attributes.author`)
  finds or creates the author with that name's slug. `attributes.author` is set
  to the name when the item has no plain byline and the schema accepts an
  `author` property (declared, or `additionalProperties` is not `false`).
  A name without ASCII letters (`李明`, `Ελένη`) gets a stable `author-<hash>`
  slug instead of failing; a name over 255 characters is kept as plain text only.
* **Events: one per batch.** No `entity.created`, `entity.updated` or
  `entity.published` fires for imported rows; the service emits a single
  `import.completed` (plugins: `on_import_completed`, which is how the
  webhooks deliver `import.completed`; payload `{source, created, updated, failed}`). The
  rendered-page and sitemap caches are purged by that event, and the link index
  of the stored rows is refreshed in the background.
* **No redirects.** Unlike an editor's save, an import does not write
  `entity-move` redirects when a re-run changes a slug or parent; the importer
  owns the redirect list for its source.
