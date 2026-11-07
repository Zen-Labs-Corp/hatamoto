---
title: Build a form
nav_title: Build a form
description: Design a contact form on the Build tab, try it as a visitor would, then set what happens after someone sends it.
next: media-menus-redirects.md, settings-users-email.md
---

In this guide you design a form on the **Build** tab, check how it behaves with **Try it**, and choose what happens when someone sends it. You end with a saved form that is ready to place on a page.

## Before you start

- An owner or editor account. If you are new to the admin, take the [tour of the admin](tour.md) first.
- JavaScript turned on in your browser. The Build tab is a visual designer and does not work without it. The **Settings** and **Share and embed** tabs work without it.

## Steps

### Create the form and add fields

1. Open **Forms** (`<admin>/forms`), press **New form**, pick a template, give it a name such as "Contact" and press **Create form**. The form opens on its **Build** tab at `<admin>/forms/design/{id}`. The templates are Blank, Contact, Quote request, Newsletter signup, Event RSVP and Job application. Every new form starts **Paused**, so nothing is accepted until you set it to **Active**.

2. Add fields from the list on the left. Click a field type to add it after the selected field, or drag it to the place you want. Use the search box to find a type. Besides text, email, phone, long text, choices, date, number and web address, there are layout types (a heading, a paragraph of text and a divider), a consent checkbox and a hidden value.

3. Click a label on the form to rename it, then press Enter. Click a field to select it and change its settings in the panel on the right: **Content** (label, name, placeholder, help text and whether an answer is **Required**), **Choices** for lists, **Layout** (full, half or third width), **Rules** and **Advanced**.

4. To show a field only when another answer matches, open **Rules** and choose **Show when**. A field hidden by a rule is not required and its answer is not stored.

5. Reorder by dragging a field, or select it and press Alt with the Up or Down arrow. Use **Duplicate** or **Delete** on the selected field. Deleting shows a message, and Ctrl+Z (Cmd+Z on a Mac) brings the field back.

6. Press **Save**, or Ctrl+S (Cmd+S). The bar shows "Saved" when it is done. Changing tab or pressing the back arrow saves first; if the save fails, the page stays where it is and shows why.

### Try it and set what happens next

7. Switch **Edit** to **Try it** above the form. You can fill it in as a visitor would, and see the same messages a real visitor gets for a missing or wrong answer. Nothing is sent. Use **Desktop**, **Tablet** and **Phone** to see how the widths wrap.

8. Open the **Settings** tab to set the button text, the success message, an optional page to send the visitor to, the addresses to email, and whether to keep submissions in the admin. Press **Save**. Settings has the same bar as Build, with Undo and Redo. Leave the recipients empty to email the site address. Keep submissions for 30 days, 90 days, a year or **Never** (the default). **Don't store the visitor's IP** keeps addresses out of the stored submissions. With storing switched off, submissions are emailed but not kept in the admin, and nothing about the visitor is stored (no IP address, browser, page or referrer); the admin only sees that a send arrived. **Duplicate form** makes a paused copy.

9. Set the status from the bar on the Build tab, or on Settings: **Active** accepts submissions, **Paused** shows nothing and accepts nothing, and **Archived** retires the form.

10. To place the form on a page, add the **form** element in the page builder, or a contact-form section if your theme has one. Open the **Share** tab for other ways to share it, described below.

## Check it worked

Reload the Build tab: your fields are in the order you left them. Open **Submissions** and send a test through the page that shows the form; the answer appears in the list.

### Share tab

The **Share** tab (`<admin>/forms/design/{id}/share`) shows where the form is used and how to share it. Each copy box has a **Copy** button.

- **Used on.** A table of the pages that show the form, through the form element or a shortcode. For each page it shows the views, submissions and conversion over the last 30 days; a page with no visits shows a dash. A form placed in a theme part or a block is listed by its area name, such as "Footer", without a path. Pages that only reuse a block holding the form are not listed individually.
- **Shortcode.** Type `[form slug="contact"]` in a rich text widget, a rich text field or a page body to show the form there. On its own line it is replaced by the form. A form that is Paused, Archived or does not exist shows nothing to visitors, so the shortcode never leaves a broken form behind.
- **Link.** Every Active form has a page of its own at `/f/{slug}` in your theme, which search engines are asked not to index. A Paused or Archived form answers with the not found page. If a page or redirect already uses a path under `/f/`, that page or redirect is served and the form page does not replace it. The tab says the link is paused when the form is not Active.
- **Endpoint and HTML.** The address a hand-built form posts to, and generated HTML you can paste into a page you build yourself. The HTML includes the hidden honeypot field, unless you turned the honeypot off in Settings. It cannot carry a captcha: a form that uses one must be shown with the form element or the shortcode.

### Submissions tab

The Submissions tab shows four numbers above the list: new, total, the last 30 days, and conversion. Conversion is the submissions divided by the visits that landed on the pages the form was sent from, over the last 30 days. It is not every page view, and it reads "Not available" when there are no visits to count.

Search looks in the answers. The status filter narrows the list, and **Export CSV** exports exactly what the search and filter show. Tick rows to mark them read, move them to spam, or delete them. Click a row to open it in a side drawer: opening a new submission marks it read. Press `j` and `k` to move between submissions and `Esc` to close the drawer. The drawer has **Reply** (an email to the person who sent it, when the form asks for an email address), **Mark as spam**, **Mark unread**, **Delete**, and a **More actions** menu with **Archive** and **Forward by email** (shown when the server can send email). Forwarding a submission by email reports back on the tab. Old links to a submission, including the ones in notification emails, open it in this tab.

If you see "This form was changed in another tab", someone else (or another tab of yours) saved the form after you opened it. Press **Reload** to see their version. Your unsaved edits on this tab are not applied, so make them again.

Keyboard shortcuts on the Build tab, when you are not typing in a box:

| Keys | What it does |
|---|---|
| Alt + Up or Down | Move the field |
| Ctrl/Cmd + D | Duplicate the field |
| Delete | Delete the field |
| Up or Down | Select the previous or next field |
| Esc | Deselect |
| Ctrl/Cmd + Z, Ctrl/Cmd + Shift + Z or Ctrl + Y | Undo and redo |
| Ctrl/Cmd + S | Save (works while typing too) |
| ? | Show the list |

The old `<admin>/forms/edit/{id}` address still works and opens the Build tab. Every form option is in the [Settings reference](settings-users-email.md#forms-and-submissions).
