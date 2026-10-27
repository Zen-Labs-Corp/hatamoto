---
title: Add a section
nav_title: Add a section
description: Add a new block type to your theme that editors can place on any page and fill in from a form.
next: sections.md, preview-and-dev.md
---

In this guide you add a "Notice" block to your theme: a heading, a line of text and an optional link, in an info or a warning style. At the end, editors can pick **Notice** in the page editor, fill in a form, and see it on the page in your design.

## Before you start

- The `my-theme` folder from [Build your first theme](your-first-theme.md), pushed to a development site.
- `hatamoto theme dev ./my-theme --dev` running in a terminal, so each save goes live.
- An account on that site that can edit pages.

## Steps

A **section** is a block an editor can place on a page. Each section type is two files with the same name: a template that draws it, and a **schema** that lists the fields the editor fills in. The name of the type here is `notice`. It must start with a lower-case letter and use only lower-case letters, digits and underscores.

### Write the schema

1. Create the folders for the new files:

   ```bash
   mkdir -p my-theme/sections my-theme/schemas my-theme/assets/css/sections
   ```

2. Create `my-theme/schemas/notice.json`. It is a JSON Schema, a JSON description of the fields and their types, with a few Hatamoto keys that start with `x-hm-`:

   ```json
   {
     "x-hm-section": {
       "title": "Notice",
       "description": "A short message with an optional link"
     },
     "type": "object",
     "properties": {
       "heading":  { "type": "string", "title": "Heading", "x-hm-field": "plaintext" },
       "text":     { "type": "string", "title": "Text", "x-hm-field": "plaintext" },
       "tone":     { "type": "string", "title": "Tone", "enum": ["info", "warning"], "default": "info" },
       "linkText": { "type": "string", "title": "Link text", "x-hm-field": "plaintext" },
       "linkUrl":  { "type": "string", "title": "Link address", "x-hm-field": "url" }
     },
     "required": ["heading"]
   }
   ```

   - `x-hm-section` gives the name and description editors see when they choose a section.
   - Each property becomes a field in the editor's form, labelled with its `title`.
   - `x-hm-field` says what kind of text a field holds: `plaintext` for plain text, `url` for a link address. [Sections and schemas](sections.md) lists every kind.
   - `enum` gives a fixed list of choices, and `default` the starting value.

### Write the template

1. Create `my-theme/sections/notice.html`. It must define a template named `section-` plus the type name:

   ```html
   {{define "section-notice"}}{{$c := .Content}}
   <div class="hm-container my-notice my-notice--{{default "info" $c.tone}}">
     <h2 {{hmf "heading"}}>{{$c.heading}}</h2>
     {{with $c.text}}<p {{hmf "text"}}>{{.}}</p>{{end}}
     {{with $c.linkText}}<p><a href="{{cta $c.linkUrl}}" {{hmf "linkText"}}>{{.}}</a></p>{{end}}
   </div>
   {{end}}
   ```

   - `.Content` holds the values the editor typed, one key per schema property.
   - `{{hmf "heading"}}` marks the element that shows the `heading` field. The visual editor uses these marks to let editors change the text in place. Put one on every element that shows exactly one field.
   - `cta` lets only safe link addresses through, so a pasted `javascript:` link is dropped. Pass every link through it.
   - `default "info" $c.tone` uses `info` when the field is empty.

   You do not write the outer `<section>` element. Hatamoto wraps each section in one for you.

2. Create `my-theme/assets/css/sections/notice.css`:

   ```css
   .my-notice {
     padding: 1.5rem;
     border-left: 4px solid var(--hm-primary);
   }
   .my-notice--warning {
     border-left-color: #c2410c;
   }
   ```

   Files in `assets/css/sections/` join the site's stylesheet after the other CSS files.

3. Save all files and watch the terminal. A line such as `pushed my-theme v0.1.0 (dev, unsigned)` means the theme loaded with the new section.

### Place it on a page

1. In the admin, open a page in the page builder and open the **Add** panel (the palette). It lists **Notice** (the section's `title`) in the **Theme sections** group, next to the built-in sections. [Edit a page](../admin/page-editor.md) covers the builder.

2. Insert **Notice**, select it and fill in its fields on the **Content** tab: `We are closed on Monday` as the heading, `Back on Tuesday at 9.` as the text, `warning` as the tone, `Opening hours` and `/contact` for the link.

3. Save the page, then publish it or update it.

## Check it worked

Open the page on your site. The notice shows with an orange left border. The page source holds the section like this:

```html
<section id="s1" data-hm-section="s1" data-hm-type="notice" class="hm-section hm-section--notice">
<div class="hm-container my-notice my-notice--warning">
  <h2 data-hm-field="heading">We are closed on Monday</h2>
  <p data-hm-field="text">Back on Tuesday at 9.</p>
  <p><a href="/contact" data-hm-field="linkText">Opening hours</a></p>
</div>
</section>
```

The `data-hm-` attributes are markers the editor uses to find the section and its fields; leave them as they are. The `id` and `data-hm-section` values are the id the editor gave the section, so yours will differ.

If the terminal shows `NOT PUSHED` with a message about the schema, the schema uses a keyword the editor form cannot show, such as `$ref` or `oneOf`, or an unknown `x-hm-field` kind. If it says a template is missing, check that the file defines `section-notice` and that both files are named `notice`. A schema without a template of the same name is refused.

If **Notice** is not in the list of sections, the theme with the new files is not the live one: check **Theme > Themes**.
