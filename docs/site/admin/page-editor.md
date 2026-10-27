---
title: Edit a page
nav_title: Edit a page
description: Change the text, images and layout of a page with the visual page builder, then save and publish it.
next: media-menus-redirects.md, builder-reference.md
---

In this guide you open a page, add a block, change its text and publish the result. You see the page as visitors will while you work, and nothing goes live until you publish.

## Before you start

- An owner or editor account. If you are new to the admin, read [Edit and publish content](content.md) first.
- A page to work on. To make one, open **Entities**, then **Create > Page**: one click creates a draft called "Untitled page" and opens it in the editor.

## Steps

### Open the page

1. Open **Entities**, choose **Pages** in the menu next to the list, and press **Edit** on the page's row.

2. Every page opens in the **page builder**, a visual editor with your page in the middle. A page made before the builder existed opens with its content already turned into builder elements; nothing is stored, and no new version appears, until you save. Its theme blocks, called **sections** (such as a hero banner or a contact form), show up as elements you can select and edit.

### Use the page builder

1. Find the three panes:

   - **Navigator** on the left lists every element of the page as a tree, with a **Filter** box, **Collapse all** and **Rename**. Drag a row to move it. Beside its tab is **Add**, where you pick what to insert.
   - The preview of the page in the middle. Point at an element to see a dashed outline, click it to select it, and double-click text (or press Enter) to type in it. A small toolbar next to the selected element has **Parent**, **Duplicate**, **Wrap**, **Hide**, **Delete** and **More**. Above the page, a strip shows where the element sits in the page (click a name to select that parent) and has **Fit**, **100**, **75** and **50** zoom buttons.
   - The **Inspector** on the right, with three tabs: **Content**, **Style** and **Advanced**.

   The top bar has labelled **Navigator** and **Inspector** buttons that hide each pane, and one status chip that reads, for example, "Draft · Saved 14:02", "Saving…" or "Not saved".

2. Start the page. On an empty page a **Start this page** panel offers a few starting points (a hero banner, features, a call to action, an FAQ) and **Start blank**. Pick one, or start blank and add elements one at a time.

3. Add an element. Point between two elements, or inside an empty box, and a round **+** appears: press it to pick a layout (one to four columns, two uneven columns or a grid), **Choose an element…** or **Insert a ready-made section…**. Or open the **Add** tab, or press the **Add element** button at the foot of the left pane (or `/`). Type in the search box to find an element, a pattern (a ready-made group of elements) or a block (see below). Recent choices come first. Press Enter to insert the first result after the selected element, or inside it if it is a container, an element that holds others. Shift+Enter inserts it and keeps the search open for the next one. You can also drag a tile onto the preview.

4. Search everything with the command palette. Press `Ctrl+K` (`Cmd+K` on a Mac), or the **Ctrl K** button at the foot of the left pane. One box searches elements, patterns, blocks and commands such as Undo, Tablet, History or Save as block. While you are typing inside text on the page, `Ctrl+K` still means "add a link".

5. Change its text. Select the new element, then edit its fields on the **Content** tab. To format a heading or text in place, double-click it on the page, or select it and press Enter (press Enter again to finish, or Esc to cancel; see [Formatting text on the page](builder-reference.md#formatting-text-on-the-page)). An image needs a short description (alt text) for people who cannot see it, or the **Decorative** box ticked; the panel warns you until you do one of them.

6. Change how it looks on the **Style** tab. It starts with a few **Essentials** for that kind of element (a heading shows size, weight, colour, alignment and spacing, for example) and keeps the rest under **More style options**. Colours show their plain names, such as Brand and Text, and owners can open **Edit site colours** from the colour list. A small label on each control says where its value comes from, and **Reset** removes just that value. The values come from your site's design system, so the page stays consistent. The **Element options** menu (the "…" at the top of the inspector) copies and pastes a style, and can **Restore from an earlier version…** of just that element.

7. Check other screen sizes. The device switch above the preview reads **Desktop**, **Tablet ≤1024** and **Phone ≤640**, the widest screen in pixels each one covers. On Tablet or Phone a chip reads "Editing Tablet" (or Phone) and a coloured band marks the top of the page, because Style changes then apply to that width only, so you can give phones their own spacing. Under **Show on**, three switches (Desktop, Tablet, Phone) choose where an element appears; each works on its own.

8. Undo a mistake with **Undo** in the top bar, or `Ctrl+Z` (`Cmd+Z` on a Mac). **History** lists the steps in plain words, such as "Add Heading" or "Move Button", and you can go back to any of them. Press `?` to see every keyboard shortcut.

9. Save. On a draft, **Save draft** stores your work and **Publish** makes it live. Drafts also save themselves a few seconds after each change. Once a page is published, the button becomes **Update page**, and nothing changes on the live page until you press it.

10. Open the **More** menu (the three dots) for **Versions**, **Page settings** (the address and search details), **Keyboard shortcuts**, and, for owners, **Site styles**. At the bottom, in red, is **Delete page**; it asks you to confirm first. When you edit a block or a layout part, the same entry reads **Delete block** or **Delete part**.

Two small tips show once, the first time you use the builder: one about selecting and typing, one about adding. Press **Got it** to dismiss them; they do not come back.

### Reuse what you built

1. Select an element, then open the **Add** tab and press **Save as block…** or **Save as template…**. Each opens a small window where you type a name.

   - A **block** is shared. Editing the block changes every page that uses it. Blocks are listed under **Theme > Blocks**.
   - A **template** is a copy you can insert on other pages. Later edits to the copy do not change the original.

   The same two commands are in the `Ctrl+K` palette and in the right-click menu.

### Change the look of the whole site

1. If you are an owner, open **More > Site styles**. A panel opens on the right with the site's colours (Brand, Secondary, Background, Text) and its Small, Medium and Large corners. Change a value and press **Save site styles**; the preview updates and every page picks it up. An **Accent** colour appears only when your theme uses builder colours. The **Secondary** colour matters only to themes that read it, and the default theme does not.

### On a phone

1. On a narrow screen the panes become a dock at the bottom with **Navigator**, **Add** and **Inspector**. Tap a pane to open it as a sheet. Tapping an element in the list opens its settings. **Undo** and the device switch stay in the top bar.

## Check it worked

Press **View on site** on the page's row in **Entities**, or open its address. You see your new block with the text you typed.

If the status chip reads **Changed somewhere else**, someone else saved the same page after you opened it. A banner over the top of the preview explains that your changes are still there but not saved. Choose **Keep my version** to save yours over theirs, **Load their version** to load theirs and redo your change, or **Compare in Versions** to look at both first.

If the editor shows **This page's layout cannot be read** instead of the builder, the stored layout is damaged. Press **Open the versions** and restore an earlier version, or change the page's other fields on its **Details** screen. If it shows **This page has a legacy layout**, an older page has sections whose ids (their internal names) the builder cannot use; your content and the public page are unchanged, and an administrator has to fix it (the message says how).

If a page you published still shows the old text, wait a moment and reload. If it still does, an owner can clear the **page cache**, the stored copies of pages that make your site fast, under **Settings > Performance**, see [Performance and the page cache](performance-cache.md).

Every element, setting, shortcut and limit of the editors is listed in the [Editor reference](builder-reference.md).
