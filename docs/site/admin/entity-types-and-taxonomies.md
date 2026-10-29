---
title: Entity types and taxonomies
nav_title: Types and taxonomies
description: The content list, the edit form, versions, content types, categories and tags, and people, with every option, address and rule.
---

This page lists everything about content in the admin: the list and its filters, the edit form, versions, content types, taxonomies and people. To create and publish something step by step, follow [Edit and publish content](content.md).

## Overview

Hatamoto stores every piece of content as an **entity**: a page, a blog post, a person or any other item. Each entity belongs to an **entity type** (shown to editors as a content type), whose schema decides its fields and its public address. A **taxonomy** groups entities, such as categories or tags, and each group in it is a **term**.

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

| Screen | Address | Owner | Editor |
|---|---|---|---|
| Entity list, edit form, versions | `<admin>/entities` | yes | yes |
| People | `<admin>/people` | yes | yes |
| Media library | `<admin>/settings/media` | yes | yes |
| Entity types | `<admin>/settings/types` | yes | no |
| Taxonomies | `<admin>/settings/taxonomies` | yes | no |

## The entity list

`<admin>/entities` is where you land after signing in. A secondary sidebar lists **All entities** and one entry per entity type with its entry count. Owners also get **Manage entity types**. Choosing a type (`?type=<slug>`) shows that type with its plural name as the title, one **New** button for that type and no Type column. An unknown slug falls back to All.

From All entities, the **Create** menu lists every type except reusable blocks and layout parts, which live under **Theme**. Choosing a page or a post type creates an "Untitled" draft and opens its editor; a writer's newest untouched draft of that type is reused, so double clicks do not pile up empty drafts. Any other type opens the new-entity form.

Every filter lives in the address, so a view can be bookmarked:

| Parameter | Meaning |
|---|---|
| `q` | Search titles and URLs |
| `type` | An entity type slug |
| `status` | `published`, `draft`, `scheduled` or `archived` |
| `under` | A parent entity: shows everything below it. The **Parent** filter appears when some entities have children. |
| `author` | An author (person) |
| `term` | A taxonomy term, including its descendants |
| `from`, `to` | Updated-date range |
| `sort`, `dir` | Sort column (`name`, `status` or `updated`; the default is `updated`, newest first) and direction |
| `page` | Page number |

### Row and bulk actions

| Action | Shown when | Effect |
|---|---|---|
| Edit | always | Opens the editor for the type |
| Settings | the type uses the page, section or blog editor | Opens the entity form (`<admin>/entities/{id}`) for name, slug and search details |
| View on site | published and the entity has a public address | Opens the public page |
| Unpublish | published | Moves it back to draft |
| Publish | draft or archived | Publishes it |
| Delete | always | Confirmation page, then deletes the entity and its versions |

Bulk actions are Publish, Move to draft, Archive and Delete. The checked ids are posted to `<admin>/entities/bulk`. Delete always asks first: without JavaScript you get a confirmation page (the form posts again with `confirmed=1`), with JavaScript the browser's confirm dialog opens.

## The edit form

`<admin>/entities/{id}` shows the entity form. Types that use a visual editor open it from `<admin>/entities/{id}/edit` instead: pages (and any entity with page sections) open in the page builder, and post types open in the blog editor. The admin picks the right screen for the type. See the [Editor reference](builder-reference.md).

The form is one POST and contains:

| Field | Notes |
|---|---|
| Name, Slug | The slug is the last part of the public address. |
| Status | Draft, Published, Scheduled or Archived. |
| Publish at | Used when the status is Scheduled. Typed in the site time zone (**Settings > General**). The background worker publishes the entity when it is due. |
| Parent | Shown only when the type's address pattern uses `{path}` or `{parent}` (a page tree). A search box. |
| Author | A person. **Or add a new author** creates one. |
| Terms | Categories, tags and any custom taxonomy. Owners can add a new term here; editors can only pick existing ones. |
| SEO panel | Tabs for Search (SEO title, SEO description), Social (social image), Schema and Advanced (canonical address and a switch that asks search engines not to list the page). Stored on the entity as `meta_title`, `meta_description`, `canonical_url`, `og_image` and `no_index`, the keys the public site reads, and not repeated among the type's own fields. |
| The type's own fields | Generated from the type's JSON Schema: text, long text, numbers, switches, images, lists and so on. |

Buttons for adding, moving and removing repeated rows re-render the form without saving. The form's subtitle shows the type and the current version number. Image fields and the social image use the media picker (`<admin>/media/picker`), which opens as a dialog when JavaScript is on. The page, builder and blog editors also refuse a stale save; see the [Editor reference](builder-reference.md).

## Versions

Every save creates a version.

| Address | What it does |
|---|---|
| `<admin>/entities/{id}/versions` | Lists the versions |
| `<admin>/entities/{id}/versions/{version}` | Shows one version |
| `<admin>/entities/{id}/versions/compare` | Shows the difference between two versions |
| `<admin>/entities/{id}/versions/{version}/restore` | Asks for confirmation, then restores that state as a new version |

Restoring never deletes history. A nightly job merges identical consecutive versions and prunes old ones, keeping the most recent 20 per entity and dropping automatic autosave versions before explicit ones.

## Deleting and archiving

Deleting an entity (`<admin>/entities/{id}/delete`) goes through a confirmation page and removes its version history too. Archive an entity instead to take it off the public site and keep it.

## Entity types

Owners manage types under **Settings > Entity models > Entity types** (`<admin>/settings/types`). A type has a name, a slug, a JSON Schema for its fields and settings for its public addresses.

The field editor builds the schema from rows. You can drag rows to reorder them (a drag only moves the row), group fields under headings and mark fields required. Anything the editor cannot express is kept as a `custom` row and never lost. Because Postgres does not keep key order in `jsonb`, the chosen order is stored in a root `x-hm-order` list, and an optional `x-hm-group` heading on a field groups it in the form.

| Setting | Meaning |
|---|---|
| `url_pattern` | The public address pattern, for example `/blog/{slug}` or `/docs/{path}`. A live preview is at `<admin>/settings/types/preview-pattern`. See [URL patterns](../ai-seo/url-patterns.md). |
| `archive_path`, `archive_title`, `archive_description` | The listing page for the type |
| `archive_crumb_label` | The section label in an entry's breadcrumb (falls back to the archive title, then the type name; the BreadcrumbList data uses it) |
| Sitemap group and JSON-LD type | How the entries appear in the sitemap and in structured data |
| Editor | Form, or Post editor to edit entries like blog posts. The types `post`, `article`, `blog_post` and `blog` always use the post editor. |

**Built-in types are read-only, except `person`.** You can add fields to `person` and change its settings, but its core fields (job title, short bio, bio, photo, public, public email, website, LinkedIn, X, GitHub, expertise, on team) are locked, and the type cannot be deleted. The server enforces this for the REST API too.

**Changing an address pattern keeps old links working.** If you change a type's pattern (for example from `/author/{slug}` to `/team/{slug}`), Hatamoto writes a 301 redirect from each published entity's old path to the new one. A redirect that already exists at an old path is left alone, a move that would loop is skipped, and an entity that is not public gets none. See [Redirect rules](../migrate-contribute/redirect-rules.md).

## Taxonomies

**Settings > Entity models > Taxonomies** (`<admin>/settings/taxonomies`, owners) manages categories, tags and any custom kinds. Each kind is a tab, and its terms are a tree you can nest. Open a term at `<admin>/settings/taxonomies/terms/{id}` to rename it, change its parent, merge it into another term or delete it.

- A term's edit page has a **Search appearance** card: the SEO title, SEO description, canonical address, social image and the switch that asks search engines not to list the archive, in the same tabs as the entity form. Terms have no body, so the content checks (such as internal links) are not shown. What you save is used by the term's public archive page: the title and description go into its head, the canonical address only on the first page of the archive, and "do not list" adds `noindex`. A description is never copied from the term's own description; leave the field empty and the archive has none, as before.
- A merge moves every entity to the target term and sends the old archive address to the new one with a redirect.
- Renaming a term or giving it another parent also redirects the archive paths of the term and its children.
- Editors can assign existing terms from the entity form but cannot create terms there. Owners can.

## People

People live at `<admin>/people`, reached from the Entities list. The list shows every `person` entity with search, a team filter (`?team=yes` or `?team=no`) and sorting. **Add person** opens a dialog, or `<admin>/people/new` without JavaScript. A person is edited in the ordinary entity form, so fields you add to the `person` type appear there. A person never opens in the page builder.

| Field | Meaning |
|---|---|
| Name, photo, job title, short bio, links | The profile |
| Public | The person gets a profile page, by default at `/author/{slug}`, and appears in the sitemap. A person who is not public has neither. |
| On team | Marks the person as part of the team |

In the blog editor's author picker, typing a new name creates a person who is not public and has no login.

### Your own profile

`<admin>/profile` edits the person linked to your login: name, photo, job title, short bio, LinkedIn and the **Public profile** switch. You can also change your password there (at least 12 characters) and sign out on all devices. Until your person has a name and a photo you are sent to `<admin>/profile/complete` after signing in.

### Possible duplicates

The **Possible duplicates (N)** button opens `<admin>/people/duplicates`. Each group has a Merge form (owners and editors) that posts to `<admin>/people/merge`. A merge:

- credits every post of the dropped person to the kept person, then deletes the dropped person;
- moves the dropped person's login to the kept person when the kept person has none; two people who both have a login cannot be merged;
- keeps the kept person's **Public** and **On team** choices;
- fills empty profile fields of the kept person from the dropped one, except when the kept person is public and the dropped one was hidden: then nothing is copied, so a hidden person's details never become public;
- writes a 301 redirect from the dropped person's profile page to the kept one when both were public.

### Upgrading from authors

Older sites had plain authors instead of people. The upgrade migrations create one person for every author, make a person public only if the author had published posts, point posts at their person and link each user to the person with the same name. Nothing is deleted. For what a rollback sees, see [Upgrading a self-hosted install](../updates/upgrading-self-hosted.md).
