---
title: Take a tour of the admin
nav_title: Tour
description: Sign in to your site's admin, find your way around the menus and learn what your account can do.
next: content.md, page-editor.md
---

In this guide you sign in to the admin, the private part of your site where you write and manage everything, and look at each main screen once. At the end you know where your content, the look of the site and the settings live, and what your account may change.

## Before you start

- A running Hatamoto site. If you have not set one up yet, follow [First run](../getting-started/first-run.md).
- An account on the site: an email address and a password. The owner can make one for you, see [Users and API keys](users-and-api-keys.md).
- A web browser. The admin also works with JavaScript turned off and on a phone.

## Steps

### Sign in

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Run `hatamoto admin-url` on the server, or ask whoever runs it. It prints the full link when your site address is set. Otherwise it prints only the path, so put your site address in front, for example `https://example.com/k7qm-x2rf-9tdc`. If you are not signed in, the sign-in page opens at `<admin>/login`.

2. Sign in with your email address and password. You arrive at **Entities** (`<admin>/entities`), the list of everything on your site. An **entity** is a piece of content: a page, a blog post, a person or any other item.

   - If the site was installed a moment ago and nobody has finished setting it up, `<admin>/setup` opens a short wizard instead. The server logs the full setup address when it starts. It asks for the **setup code**, a one-time code the server prints to its log so only someone with access to the server can create the first owner. [First run](../getting-started/first-run.md) walks you through it.

3. If the admin asks you to complete your profile, add your name and a photo, then save. The admin shows this step at `<admin>/profile/complete` until your profile has both.

### Look around

1. Look at the sidebar on the left. It has up to five items:

   | Item | What you find there | Who sees it |
   |---|---|---|
   | **Entities** | your pages, posts and other content, and **People** | owners and editors |
   | **Theme** | how the site looks: themes, colours, menus, reusable blocks | anyone who may open at least one Theme screen |
   | **AI analytics** | which AI tools and search engines read your pages | owners and editors |
   | **Plugins** | add-ons that give the site extra features | owners |
   | **Settings** | everything about the site as a whole | anyone who may open at least one Settings screen |

2. Look at the bottom of the sidebar. Your picture opens your profile at `<admin>/profile`, the next button switches between light and dark colours, and the last one signs you out.

3. Open **Settings**. A second, narrower menu appears with the Settings sections: General, Email, Media, Entity models, Forms, Redirects, Internal linking, Users, API, Webhooks, Performance, Updates and Import & export. You only see the sections your account may open. The [Settings reference](settings-users-email.md) describes each one.

4. Open **Theme**. It has its own second menu too: Themes, Colours and fonts, Design system, Style presets, Layout parts, Blocks, Templates and Menus.

5. Check which **role** your account has. A role is what an account may do. If an editor opens a screen that is for owners only, the admin shows a **Not allowed** page. There are two roles:

   - An **owner** can do everything: content, users, settings, themes and plugins. A site always keeps at least one owner: the last owner can never be deleted or made an editor.
   - An **editor** works on content: pages and posts, media, menus, forms and the messages they receive, redirects, internal links, reusable blocks, templates and AI analytics.

### See how the admin behaves

1. Try a list. Open **Entities**, type a word in the search box and choose a status. The address in your browser changes to hold your search and filters, so you can bookmark the view or send it to a colleague.

2. Notice how the admin protects you from mistakes:

   - **Delete** never acts on one click. It opens a confirmation first, and deleting many rows at once asks twice.
   - If you fill in a form wrongly, the same form comes back with a list of the problems at the top. When a save works, a short message confirms it.
   - Some secrets are shown once only: a new API key, an invitation link, a password reset link and the signing secret of a webhook (a web address your site calls when something changes). Copy them when they appear, because you cannot open them again.

3. If you use a phone or a narrow window, the sidebar becomes a bar at the top of the screen, and the second menus of Settings and Theme become a drop-down list with a **Go** button.

## Check it worked

You can answer three questions: where your content list is (**Entities**), where the menus of your site are (**Theme > Menus**) and which role you have. If you cannot see **Plugins** or most of **Settings**, you are signed in as an editor, which is normal.

If you forgot your password, open `<admin>/forgot` and enter your email address. When the site can send email, you receive a reset link that works once, for one hour. When it cannot, ask someone with access to the server to run this command there, with your address:

```bash
hatamoto user reset-password --email you@example.com
```

For keyboard use, screen readers and how the admin keeps your account safe, see [Accessibility and security](accessibility-and-security.md). What the admin aims for and how it is tested is in the [Accessibility statement](accessibility.md).
