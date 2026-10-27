---
title: Add media, menus, forms and redirects
nav_title: Media, menus, redirects
description: Upload images, build your site's navigation menu, add a contact form and send an old address to a new page.
next: users-and-api-keys.md, ../migrate-contribute/redirect-rules.md
---

In this guide you do four everyday jobs that affect the whole site: upload an image, build a menu, add a contact form and forward an old address to a new page. Owners and editors can do all of them.

## Before you start

- An owner or editor account. If you are new to the admin, take the [tour of the admin](tour.md) first.
- An image file on your computer, and a page or two already published (see [Edit and publish content](content.md)).

## Steps

### Upload an image

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Open **Settings > Media** (`<admin>/settings/media`) and press **Upload**. Choose up to 20 files at once. Each file may be up to 10 MB unless the server's administrator changed that limit.

2. Open the image you uploaded. Fill in its **Alt text**, a short description for people who cannot see the picture, and a **Caption** if you want one, then save. The library shows a **Missing** badge on every image that still has no alt text. You rarely need the library to insert a picture later: image fields and both editors open it as a picker, with search and an upload button.

### Build a menu

1. Open **Theme > Menus** (`<admin>/theme/menus`) and press **New menu**. Give it a name, such as "Main", and a **Location**: Header, Footer, Sidebar or No location. Your theme decides which locations it shows.

2. Add an item. Give it a **Label**, the text visitors see, and choose what it links to. Tick **Open in a new tab** only if visitors should keep your site open.

   - **Page or post**: type a few letters and pick one of your pages.
   - **Custom address**: any web address or path, such as `https://example.com/shop` or `/contact`.
   - **Category or tag**: the page that lists everything in that group.

3. Arrange the items with **Move up**, **Move down**, **Indent** (put an item under the one above it) and **Outdent**. With JavaScript on, you can also drag them; sub-items move with their parent.

4. Press **Save menu**. The whole menu is saved at once, or not at all if something is wrong.

### Add a contact form

1. Open **Settings > Forms** (`<admin>/settings/forms`) and press **New form**. Give it a name, such as "Contact", and press **Create form**.

2. Press **Add field** for each question. For a contact form, add a name (single-line text), an email address and a message (multi-line text), and tick **Required** where an answer is needed.

3. Under **After someone sends it**, fill in the **Success message** and the address in **Email notifications to**. Notification email works only once email is set up (**Settings > Email** or the server's `SMTP_*` settings); see [Set up email](../operate/email.md). Set **Status** to Active and press **Save form**.

4. Put the form on a page. In the page builder, add the **form** element and choose your form, or add a contact-form section if your theme has one.

5. Read what visitors send under **Settings > Forms > Submissions** (`<admin>/settings/submissions`). Messages that the spam rules catch are kept with the status Spam; move one back if it was a mistake.

### Forward an old address

1. Open **Settings > Redirects** (`<admin>/settings/redirects`) and press **New redirect**. A **301 redirect** is a permanent forward from an old address to a new one: visitors and search engines that ask for the old address go to the new one.

2. Fill in **From**, the old path such as `/old-services`, and **To**, the new path such as `/services`. Keep **Type** at **301: moved permanently**, then press **Save redirect**.

3. Find addresses that need a redirect. The **404 log** tab lists addresses visitors asked for that do not exist. Each row has **Create redirect**, **Ignore** and **Remove**. The **Explain a URL** tab shows, step by step, what happens to any address you type.

When you change the address of a published page, Hatamoto adds the redirect from its old address for you, so links keep working.

## Check it worked

- Open your site: the menu shows in the location you chose, with your items.
- Open `https://example.com/old-services`: the browser lands on `/services`.
- Send a test message through your form: it appears under **Submissions**.

If the menu does not show, your theme may not print that location: choose another **Location**, such as **Header**, and save again. If a redirect does nothing, check that **From** starts with `/` and matches the old address exactly, capital letters included, and use **Explain a URL** to see which rule wins.

Every setting of these screens is in the [Settings reference](settings-users-email.md). Wildcard redirects and the order in which redirect rules are checked are in [Redirect rules](../migrate-contribute/redirect-rules.md).
