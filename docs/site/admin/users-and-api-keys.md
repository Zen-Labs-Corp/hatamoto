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
   - **Author** writes and publishes their own content. They can also delete and restore their own entries.
   - **Contributor** writes drafts and edits their own; an editor or reviewer publishes them. They cannot publish, also not by scheduling.
   - **Reviewer** edits and publishes anyone's content, but cannot create or delete entries.
   - Author, Contributor and Reviewer can upload media and change or delete only the media they uploaded. "Their own" means the person who created the entry; entries with no known creator are changed only by owners, editors and reviewers.
   - **Read-only** sees the admin and changes nothing. They keep their own profile, password and two-step sign-in.

   People with the newer roles never see plugin pages. An API key made for a user acts with at most that user's role.

3. Press **Send invitation**. The users list opens with the invitation link in a banner. The link works once and expires after 7 days.

4. Copy the link now, because it is shown only once. If the site can send email, your colleague also receives it by email and the banner says so. If not, send the link yourself through a private channel.

5. Your colleague opens the link, enters their name, chooses their own password (at least 12 characters) and is signed in.

Until they accept, the invitation appears under **Pending invitations**, with **Send a new link** and **Revoke**. To set a password for someone yourself instead, use **Add with a password instead** in the invitation form.

### Access that ends

For a contractor or a guest, set **Access ends on** in the invitation form or on the person's edit page. Their access stops at the end of that day in the site time zone. The date must be today or later.

- From then on sign-in is refused with the usual neutral message, and anyone already signed in is signed out on their next request.
- API keys that person created stop working at once. The hourly job then deletes their sessions, revokes the keys and records the change in the audit log as `user.access_ended`.
- Three days before the end, the other owners get an email, when the site can send email.
- To restore access, choose a later date or tick **Clear date** on the edit page and save. Old sessions and keys do not come back, even when you extend the date before the hourly job has run: saving the new date removes them. The person signs in again and new keys are made.
- An owner with an end date needs another owner without one. The form says so if that is not the case.
- Only an owner without an end date can set, extend or clear end dates. An owner who has an end date cannot change any date, their own included, and can only add, invite or promote someone whose end date is set and no later than their own.
- An end date limits sign-in. It does not contain a malicious owner: any owner can still create a password reset link for another owner, install plugins and change settings while their access lasts. A reset link does not extend anyone's access, and a user whose end date has passed still cannot sign in after a reset. Give an end date to people you would trust with owner access for that long.
- The JSON users endpoints take `access_ends_at` (an RFC 3339 time with a zone; `null` clears it), owners only.

### Change or remove a person

1. Find the person in the users list. Each row has three actions. The last owner can never be deleted or made an editor: make someone else an owner first.

   - **Edit** changes the name, email and role.
   - **Reset password** creates a one-time link that works for one hour. It is emailed when the site can send email, and always shown to you once.
   - **Delete** removes the account after a confirmation. Their content stays on the site.

### Create an API key

1. An **API key** is a secret string that a program sends with each request to act on your site. Its **scope** limits what it may do. Open **Settings > Integrations > API keys** (`<admin>/settings/api-keys`) and press **New API key**.

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

### Change SEO settings with a key

A key with **Change SEO settings** can edit the site's search settings. Only the fields you send change, and `null` removes an entry from a list of settings. This example turns the 404 monitor off (stored as `disabled`) and sets the title separator:

```bash
curl -X PATCH -H "X-API-Key: <your-key>" -H "Content-Type: application/json" \
  -d '{"monitor404": {"disabled": true}, "separator": "-"}' \
  https://example.com/api/v1/settings/seo
```

A wrong field answers `422` with a `fields` list that names each one. **Read SEO settings** is enough for `GET` on the same address.

### Edit safely when several programs write

Every entity has a version that counts the versions of its content (its `attributes`). `GET /api/v1/entities/{id}` and every successful update, status change and restore return it as the `ETag` header, a strong tag such as `"9"` (creating an entry sets none). Send it back as `If-Match` and the change is applied only if nobody changed the entry in between:

```bash
curl -i -H "X-API-Key: <your-key>" https://example.com/api/v1/entities/<id>
# ETag: "9"

curl -X PATCH -H "X-API-Key: <your-key>" -H "Content-Type: application/json" \
  -H 'If-Match: "9"' -d '{"attributes": { ...every attribute you read, with your change... }}' \
  https://example.com/api/v1/entities/<id>
```

With `application/json`, `attributes` is replaced as a whole, so send back everything you read with your change. Per-entry SEO fields (such as `meta_title`) are attributes too and advance the version.

If the entry has moved on, nothing is written and the answer is `412` with the current version, also as the `ETag` header:

```json
{"error":"entity changed: current version is 10","current_version":10}
```

Read the entry again, redo the change and retry. The check is atomic with the write: of two writers holding the same tag, exactly one succeeds.

To change only some attributes, send a merge patch instead:

```bash
curl -X PATCH -H "X-API-Key: <your-key>" -H "Content-Type: application/merge-patch+json" \
  -H 'If-Match: "9"' -d '{"attributes": {"room": "B", "notes": null}}' \
  https://example.com/api/v1/entities/<id>
```

- Only `attributes` merge; other fields work as usual.
- Only the keys you send change. `null` removes a key, nested objects merge and arrays replace whole. `"attributes": null` is refused with `400`.
- `PUT` always replaces `attributes` as a whole.
- The merged result is validated like a full write. A merge that removes a required attribute answers `400` and names it.
- Send `If-Match` with a merge. Without it a merge can overwrite another program's change to a different attribute.

- `If-Match` works on `PUT` and `PATCH /api/v1/entities/{id}`, `PATCH /api/v1/entities/{id}/status` and `POST /api/v1/entities/{id}/restore/{version}`. On a restore, the `{version}` in the address is the version to restore and `If-Match` is the current version you expect.
- The header is optional. Without it a change is applied as before. `If-Match` on `DELETE` is ignored.
- Send one tag (`"9"`) or `*` (any existing entry). When the response is compressed, the tag may arrive weakened as `W/"9"`; send it back exactly as received, both forms mean version 9. A list of tags or anything else malformed answers `400`.
- The tag follows the attribute (content) version. Changes to the name, slug, locale, the separate `metadata` column, parent, byline and status do not advance it, so two such changes made with the same tag can both succeed. Use the tag to protect content edits.
- A change that only moves the entry (`position`, optionally with `parent_id` or `clear_parent`) does not check the tag.
- A restore never overwrites an edit stored while it ran. Without `If-Match` (or with `*`) it answers `409` with `current_version`; with a stale tag, `412`.
- `If-Match: *` on an entry that does not exist answers `404`.
- The API sends no CORS headers, so a web page on another origin cannot read the `ETag`.

### Entries over the API

- `GET /api/v1/entities` answers 20 entries per page. `?limit=` takes 1 to 100 and clamps a larger number to 100; `?page=` walks the pages. `per_page` is refused, the parameter is `limit`.
- Every other list follows the same rules for its page size: media (`limit`, default 50), entry versions (`limit`, 50), form submissions (`limit`, 50), section templates (`limit`, 20), redirects and the 404 log (`per_page`, 50), link rules (`per_page`) and the public knowledge graph (`per_page`, 50). A size over 100 is clamped to 100, and the answer's `limit` or `per_page` shows the size used. A size or `offset` that is not a whole number in range, a `page` below 1, or the other spelling of the size (`per_page` where the list takes `limit`, or the reverse) is answered with `400` and a message naming the parameter, never replaced by the default.
- An entry's `status` changes only through `PATCH /api/v1/entities/{id}/status`. A `status` member in a create or update body is refused rather than ignored.

### Turn on two-step sign-in

Two-step sign-in asks for a second proof after the password. Each person sets it up under **Profile > Security**, after confirming their password.

1. Open **Profile > Security** and choose **Set up authenticator app**. Scan the QR code with an authenticator app, or type the key, then enter the six digit code the app shows.
2. Save the ten recovery codes that appear. They are shown once, and each works once if you lose the app.
3. Optionally choose **Add a passkey** to sign in with your device. Passkeys belong to the site address (the host of your site, not the admin address), so they stop working if the site address changes.

At sign-in, a person with a second factor enters the password, then a code, a recovery code or a passkey. Wrong codes count toward the same lock as wrong passwords: five in a row lock the account for 15 minutes. Confirming your password for a sensitive action (sudo mode) also asks for the code when you have an authenticator app. A person who has only passkeys confirms with the password alone.

Owners choose who must use it under **Settings > Users > Sign-in**: optional, required for owners, or required for everyone. A person who is required to use it and has not set it up is sent to **Profile > Security** until they do. The users list has a **2-step** column. A script that signs in over the JSON API sends the code in a `code` field. A person the policy requires to use it cannot get a JSON session by password alone until they have set it up. API keys are not affected.

If someone loses their device and their recovery codes, run `hatamoto user reset-2fa --email E` on the server. They can then sign in with the password and set it up again.

## Check it worked

- Your colleague appears in the users list with the role you chose, and their **Last sign-in** shows a date once they have signed in.
- The `curl` command prints a JSON list, and the key's **Last used** column shows when it was used.

If `curl` prints `{"error":"invalid api key"}`, the key was mistyped, revoked or expired, or the person who created it no longer has a role that may use keys. If it prints `{"error":"forbidden"}`, the key does not have the scope that address needs: create a key with the right **Access**.

If your colleague never receives the email, set up outgoing email at **Settings > Email** (see [Set up email](../operate/email.md)) or send them the link yourself. Every scope and the server commands for users and keys are in the [Settings reference](settings-users-email.md).
