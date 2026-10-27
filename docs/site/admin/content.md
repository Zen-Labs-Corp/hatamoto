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
   - **Search and social**: the title and description shown in search results, a canonical address (the main address, when the same content has several), a social image for when the page is shared, and a switch that asks search engines not to list the page.
   - The type's own fields, such as text, images, numbers and lists. Image fields open the media library as a picker.

6. Press **Save**. Every save creates a version, so nothing you replace is lost.

### Publish it

1. Set **Status** to **Published** and save again. You can also publish from the list, with the row's **Publish** action.

2. To publish later instead, set **Status** to **Scheduled** and fill in **Publish at**. The time is in the site's time zone, which an owner sets under **Settings > General**. Hatamoto publishes the item by itself when the time comes.

### Find, restore and remove content

1. Find your item again. Back in **Entities**, use the search box (it searches titles and addresses), the **Status** filter and the extra filters for author, term and date. The browser address keeps your filters, so you can bookmark the view.

2. Act on several items at once. Tick the rows, then choose **Publish**, **Move to draft**, **Archive** or **Delete**. Each row also has its own actions: **Edit**, **Settings** (the name, address and search details of a page or post), **View on site** once it is published, **Publish** or **Unpublish** (back to draft) and **Delete**.

3. Open an earlier version. Open the item, then its **Versions** tab (`<admin>/entities/<id>/versions`, where `<id>` is the long code in the item's admin address). You can view any version, compare two of them and **restore** one. Restoring asks you to confirm, then saves that older state as a new version, so history is never deleted.

4. Remove an item. **Delete** asks for confirmation and removes the item with all its versions. To take something off the public site but keep it, choose **Archive** instead.

## Check it worked

Your item shows **Published** in the list, and **View on site** opens it at its public address, for example `https://example.com/services`.

If the public address shows "not found", the item is probably still a draft or scheduled for later: check the **Status** column. If the address is different from what you expected, the content type decides the pattern; an owner can see it under **Settings > Entity models**, described in [Entity types and taxonomies](entity-types-and-taxonomies.md).

Editors can choose existing terms but cannot create new ones. Ask an owner to add a category or tag, or to change a content type's fields.
