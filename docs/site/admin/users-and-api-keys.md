---
title: Invite people and create API keys
nav_title: Users and API keys
description: Invite a colleague to the admin with the right role, and give a script or another service its own key to your site.
next: webhooks.md, ../operate/email.md
---

In this guide you invite a colleague to the admin and create a key that lets a program, such as a script or another service, read your content. At the end your colleague can sign in, and the program has a key that you can withdraw at any time.

## Before you start

- An owner account. Only owners can manage users and keys.
- Your colleague's email address.
- For the key test: a terminal with `curl`, on any computer that can reach your site.

## Steps

### Invite a person

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Open **Settings > Users** (`<admin>/settings/users`) and press **Invite user**.

2. Enter your colleague's **Email** and choose a **Role**, which decides what their account may do. Give the owner role only to people who must change settings.

   - **Editor** manages content: pages, posts, media, menus, forms, redirects and analytics.
   - **Owner** can do everything an editor can, plus users, settings, themes, plugins, API keys and content types (the kinds of content, such as pages and posts).

3. Press **Send invitation**. The users list opens with the invitation link in a banner. The link works once and expires after 7 days.

4. Copy the link now, because it is shown only once. If the site can send email, your colleague also receives it by email and the banner says so. If not, send the link yourself through a private channel.

5. Your colleague opens the link, enters their name, chooses their own password (at least 12 characters) and is signed in.

Until they accept, the invitation appears under **Pending invitations**, with **Send a new link** and **Revoke**. To set a password for someone yourself instead, use **Add with a password instead** in the invitation form.

### Change or remove a person

1. Find the person in the users list. Each row has three actions. The last owner can never be deleted or made an editor: make someone else an owner first.

   - **Edit** changes the name, email and role.
   - **Reset password** creates a one-time link that works for one hour. It is emailed when the site can send email, and always shown to you once.
   - **Delete** removes the account after a confirmation. Their content stays on the site.

### Create an API key

1. An **API key** is a secret string that a program sends with each request to act on your site. Its **scope** limits what it may do. Open **Settings > API** (`<admin>/settings/api-keys`) and press **New API key**.

2. Fill in the form, then press **Create key**:

   - **Name**: what the key is for, such as "Price list script".
   - **Access**: tick only what the program needs. For this guide, tick **Read entities**, which lets the program read your content and nothing else.
   - **Expires**: Never, In 30 days, In 90 days or In a year.

3. Copy the key from the banner. It is shown once and cannot be shown again.

4. Test it from a terminal, with your key in place of `<your-key>` and your own site address. The answer is a list of your content in JSON, a plain text format that programs read.

   ```bash
   curl -H "X-API-Key: <your-key>" https://example.com/api/v1/entities
   ```

5. Store the key where the program can read it, and nowhere else. The key acts with the rights of the person who created it, up to its scopes, so treat it like a password.

6. When a program no longer needs its key, use the key's row in the list: **Revoke** stops it at once, **New secret** replaces it with a fresh one (the old secret stops working), and **Delete** removes it.

## Check it worked

- Your colleague appears in the users list with the role you chose, and their **Last sign-in** shows a date once they have signed in.
- The `curl` command prints a JSON list, and the key's **Last used** column shows when it was used.

If `curl` prints `{"error":"invalid api key"}`, the key was mistyped, revoked or expired, or the person who created it is no longer an owner or editor. If it prints `{"error":"forbidden"}`, the key does not have the scope that address needs: create a key with the right **Access**.

If your colleague never receives the email, set up outgoing email at **Settings > Email** (see [Set up email](../operate/email.md)) or send them the link yourself. Every scope and the server commands for users and keys are in the [Settings reference](settings-users-email.md).
