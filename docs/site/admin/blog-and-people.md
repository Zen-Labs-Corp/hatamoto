---
title: Write a post and add people
nav_title: Blog and people
description: Write and publish a blog post, check how it will show in search results, and credit it to a person with a public profile page.
next: media-menus-redirects.md, ../ai-seo/control-ai-crawlers.md
---

In this guide you write a blog post, check how it will look in search results, publish it and credit it to an author. You also add a person, so the author has a profile page on your site.

## Before you start

- An owner or editor account. If you are new to the admin, read [Edit and publish content](content.md) first.
- The text of your post, and an image if you want one.

## Steps

### Write the post

1. Open **Entities**, then **Create > Post**. A draft called "Untitled post" opens in the blog editor: the title at the top, the body below it, and a sidebar on the right.

2. Click the title and type your own, then write the body. Three tools help you:

   - Type `/` at the start of a line for a menu of blocks: headings, lists, a quote, a table, an image, a video, a callout, a FAQ block and more.
   - Select some text for a small menu with bold, italic, link and code.
   - Typing shortcuts work too: `## ` at the start of a line makes a heading and `* ` starts a list.

3. Add an image by pasting it or dropping it into the body. It uploads to the media library, with a progress bar. Fill in the description (alt text) under the picture, for people who cannot see it, and a caption if you want one.

4. Fill in the sidebar:

   - **Slug**, the short name in the post's address, such as `hello` in `/blog/hello`.
   - **Featured image** and **Excerpt**, the short summary shown in lists of posts.
   - **Categories and tags**, the groups the post belongs to. Owners can create a new one here; editors pick from the existing ones.
   - **Author**, covered in the next part of this guide.

5. Fill in the **SEO** panel: on its **Search** tab a **Focus keyword** (the phrase you want the post to be found for), the **SEO title** and **SEO description** that search results show; on its **Advanced** tab the **Include in /llms.txt** switch, which lists the post in a file that AI tools read.

6. Read the checks in the panel below. A few seconds after you stop typing, it shows a preview of the search result, whether the title and description have a good length, whether the keyword appears in the right places, and tips that help AI assistants quote your answer.

7. Press **Preview** to see the post as visitors will, at **Desktop** or **Mobile** width.

8. Press **Save draft** to keep working later, or **Publish** to make it live. While the post is a draft, the editor saves it by itself a few seconds after you stop typing. Once it is published, the button becomes **Update**, and the live post changes only when you press it.

### Credit an author

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Open the **Author** picker in the sidebar and choose a person, or the site's own name for posts from the organisation. To credit someone new, type their name: Hatamoto creates a person who has no profile page and cannot sign in.

2. Open **Entities > People** (`<admin>/people`) to see everyone. Press **Add person** and fill in the name, photo, job title, short bio and links.

3. Decide on the two switches:

   - **Public** gives the person a profile page, by default at `/author/<their-slug>`, and lists them in the **sitemap**, the file that lists your pages for search engines. A person who is not public has neither.
   - **On team** marks them as part of your team, which themes can use for a team page.

4. Fix duplicates. If the same person exists twice, the People list shows a **Possible duplicates** button. Open it, choose which record to keep and press **Merge**. Posts move to the kept person, and a hidden person's details never become public through a merge.

### Edit your own profile

1. Click your picture at the bottom of the sidebar (`<admin>/profile`). Change your name, photo, job title, short bio, LinkedIn address and whether your profile is public, then press **Save profile**. On the same page you can change your password (at least 12 characters) and **Sign out on all devices**.

## Check it worked

Open the post's address, for example `https://example.com/blog/hello`. You see the post with the author's name. If the author is public, their name links to their profile page.

If a message says someone else saved the post while you were writing, choose **Overwrite with my version** to keep yours, or **Reload their version** to load theirs. If your browser closed before a save, the editor offers your unsaved text the next time you open the post.

Every block, check and timing of the blog editor is in the [Editor reference](builder-reference.md), and the rules for people and merging are in [Entity types and taxonomies](entity-types-and-taxonomies.md).
