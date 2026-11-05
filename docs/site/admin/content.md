---
title: Edit and publish content
nav_title: Content
description: Create a page, post or any other content, save it as a draft, publish it now or later, and go back to an earlier version.
next: page-editor.md, blog-and-people.md, media-menus-redirects.md
---

In this guide you create a piece of content, save it as a draft, publish it and then find an earlier version of it. The same steps work for pages, blog posts, people and any other kind of content your site has.

## Before you start

- An owner or editor account on your site. If you have not signed in before, take the [tour of the admin](tour.md) first.
- An idea of what you want to publish. The example below adds a "Services" item, but any title works.

## Steps

### Create a draft

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Open **Entities** in the sidebar (`<admin>/entities`). Hatamoto calls every piece of content an **entity**: a page, a post, a person or any other item. The list shows them all, newest change first.

2. Look at the menu next to the list. It shows **All entities** and one line for each **content type**, the kind of content, which decides its fields and its address. Pages, posts and people are content types. The number after each one is how many items it holds. Choose one to see only that type.

3. Create a new item. A page or a blog post opens straight in its editor as a draft called "Untitled page" or "Untitled post"; [Edit a page](page-editor.md) and [Write a post and add people](blog-and-people.md) cover those editors. Any other type opens the form described in the next steps.

   - From **All entities**, open the **Create** menu and choose a type.
   - From a single type, press its **New** button, for example **New page**.

4. Fill in the top of the form:

   - **Name**: the title people see.
   - **Slug**: the short name in the page's address, such as `services` in `/services`. The content type decides the rest of the address.
   - **Status**: Draft, Published, Scheduled or Archived. Keep **Draft** for now, so only signed-in people can see it.

5. Fill in the rest of the form. What you see depends on the type. Buttons that add, move or remove rows in a list do not save anything, so rearrange as much as you like, then save once.

   - **Parent**, for types where pages sit inside other pages. Type a few letters and pick the page this one belongs under.
   - **Author**, and the **terms**. A **taxonomy** is a way to group content, such as categories or tags, and a term is one group in it, such as the category "News".
   - **SEO** panel: its **Search** tab holds the title and description shown in search results, **Social** a social image for when the page is shared, and **Advanced** a canonical address (the main address, when the same content has several) and a switch that asks search engines not to list the page.
   - The type's own fields, such as text, images, numbers and lists. Image fields open the media library as a picker.

6. Press **Save**. Every save creates a version, so nothing you replace is lost.

### Publish it

1. Set **Status** to **Published** and save again. You can also publish from the list, with the row's **Publish** action.

2. To publish later instead, set **Status** to **Scheduled** and fill in **Publish at**. The time is in the site's time zone, which an owner sets under **Settings > General**. Hatamoto publishes the item by itself when the time comes.

### Unpublish at and redirect

1. Open a published item. In the editor, fill in **Unpublish at**. The time is in the site's time zone, which an owner sets under **Settings > General**.

2. Optionally fill in **When it unpublishes, send visitors to** with a path such as `/news`, or a full https address. Leave it blank and the page is simply gone.

3. Save. At that time Hatamoto moves the item back to draft by itself. If you gave a destination, the old address then redirects there (a temporary 302 redirect, listed under redirects). Publishing the item again turns that redirect off. To cancel, tick **Remove the scheduled unpublish** and save. The list shows "Published, ends" with the date for items that have one.

### Share a draft preview

1. Open a draft or scheduled item, then choose **Share preview** under **More actions**.

2. Pick how long the link works (1 hour, 1 day, 7 days or 30 days) and press **Create link**. Copy the link right away: it is shown once, and later you only see its last four characters.

3. Send the link to anyone. They see the last saved draft as it will look live, without signing in. Search engines are told not to list it. An item can have up to 10 active links. Revoke a link in the table below and it stops working at once. Published items cannot be shared. A link stops working, and answers "not found", once the item is published, archived or deleted. If the site is private (Site access), its own gate still applies to everyone who is not signed in.

### Reorder pages and posts

1. Open a type's list and choose **Reorder**.

2. Use **Move up**, **Move down**, **Indent** and **Outdent** on a row's actions. Each is a plain button, so it works without JavaScript. Indent puts the entry under the one above it; Outdent moves it back up a level. A type without nested addresses only has Move up and Move down.

3. Nesting stops at 8 levels in the address, and Indent is not offered past that. A published page that changes parent keeps its old address as a redirect.

Moving up or down does not edit the entry: no version is saved, and a page a plugin supplies stays the plugin's. Indent and Outdent change the parent, so they count as an edit: the address changes and a page a plugin supplies becomes yours, with a note on the page and a line in the audit log. Themes show the order with `order: "manual"` in a loop; nothing changes until a theme asks for it.

### Find, restore and remove content

1. Find your item again. Back in **Entities**, use the search box (it searches titles and addresses), the **Status** filter and the extra filters for author, term and date. The browser address keeps your filters, so you can bookmark the view.

2. Act on several items at once. Tick the rows, then choose **Publish**, **Move to draft**, **Archive** or **Delete**. Each row also has its own actions: **Edit**, **Settings** (the name, address and search details of a page or post), **View on site** once it is published, **Duplicate**, **Publish** or **Unpublish** (back to draft) and **Delete**.

3. Copy an item. Choose **Duplicate** in the row's actions, or under **More actions** (the three dots) at the top of the editor. The copy is a draft named "Copy of ..." with the address `-copy` added (`-copy-2` and so on when that is taken), in the same language and under the same parent, and it opens in the editor. It carries the content, the search details except the canonical address, the image, the byline and the terms. It never carries the status, the dates or the translations, so it is not linked to the original. People and reusable blocks cannot be duplicated. To copy a page into another language, use **Create copy** on the Translations tab instead.

4. Open an earlier version. Open the item, then its **Versions** tab (`<admin>/entities/<id>/versions`, where `<id>` is the long code in the item's admin address). You can view any version, compare two of them and **restore** one. Restoring asks you to confirm, then saves that older state as a new version, so history is never deleted.

5. Remove an item. **Delete** asks for confirmation and removes the item with all its versions. To take something off the public site but keep it, choose **Archive** instead.

## Check it worked

Your item shows **Published** in the list, and **View on site** opens it at its public address, for example `https://example.com/services`.

If the public address shows "not found", the item is probably still a draft or scheduled for later: check the **Status** column. If the address is different from what you expected, the content type decides the pattern; an owner can see it under **Settings > Entity models**, described in [Entity types and taxonomies](entity-types-and-taxonomies.md).

Editors can choose existing terms but cannot create new ones. Ask an owner to add a category or tag, or to change a content type's fields.

## Search and replace

Owners can change a word, a phrase or a web address across the whole site in one pass. Open **Settings > Tools > Search and replace**. Editors do not see this page.

1. Type the text to **Find** and what to **Replace with**. Leave the second box empty to remove the text.
2. Choose how to match. **Plain text** finds the text anywhere, even inside a longer word. **Whole word** skips matches that are part of a longer word. **Match upper and lower case** is on by default. There is no regular expression mode.
3. Tick the places to **Look in**: content text (including page builder text), SEO fields, other fields such as excerpts and custom fields, and menus.
4. Select **Preview**. The page lists every match with the text around it and changes nothing.
5. Untick any match you want to keep, then select the **Replace** button.

What to know before you apply:

- **Each changed item gets one new version.** Open the item's **Versions** tab to restore the earlier text. The change summary names the find and replace text.
- **Links and addresses are covered.** Matches inside a link address and an image address are replaced. Page builder structure, ids and styles are never touched, only the visible text.
- **Plugin-owned items start unticked.** Replacing in one takes ownership from the plugin, so the plugin stops updating it. Tick it only if you mean that.
- **Menus have no version history.** Menu matches can be replaced, but the old label or address cannot be restored.
- **Items edited since the preview are skipped.** If someone saves an item between your preview and your replace, that item is left alone and the result message counts it. Preview again to include it.
- **Limits.** The preview counts up to 10,000 matches and lists the first 500. If there are more, refine your search and run it again.
- **The audit log records the run**, with the find and replace text and the counts of matches, items, menu items and skipped items.
