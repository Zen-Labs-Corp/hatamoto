---
title: Change colours and fonts
nav_title: Colours and fonts
description: Set your theme's colours and fonts in CSS, let the site owner override them in the admin, and add a setting of your own.
next: preview-and-dev.md, packaging.md
---

In this guide you give your theme its own colours and fonts, see how the site owner can change them from the admin without touching your files, and add a setting of your own to the theme. You work on the `my-theme` folder from [Build your first theme](your-first-theme.md).

Hatamoto styles pages with **design tokens**, named values such as a colour or a font that the theme reuses everywhere. In CSS they are custom properties such as `--hm-primary`.

## Before you start

- The `my-theme` folder from [Build your first theme](your-first-theme.md), pushed to a development site.
- `hatamoto theme dev ./my-theme --dev` running in a terminal, so each save goes live.
- An owner account on that site, for the admin steps.

## Steps

### Set your theme's defaults in CSS

1. Open `my-theme/assets/css/my-theme.css` and add a `:root` rule at the top:

   ```css
   :root {
     --hm-primary: #0a5cff;
     --hm-font-heading: Georgia, serif;
   }
   ```

   Hatamoto builds one stylesheet from your theme: `assets/css/base.css` first, then the other files in `assets/css/` in name order, then the files in `assets/css/sections/`. The built-in theme's `base.css` sets the default values, and your file comes after it, so your values win.

2. Save the file and reload your site. Headings use Georgia, and primary buttons are blue. These are the tokens the built-in theme reads:

   | Custom property | What it styles |
   | --- | --- |
   | `--hm-primary` | the brand colour, used by primary buttons |
   | `--hm-bg` | the page background |
   | `--hm-text` | the body text colour |
   | `--hm-font` | the body font |
   | `--hm-font-heading` | the heading font |
   | `--hm-radius-sm`, `--hm-radius-md`, `--hm-radius-lg` | small, medium and large corners |

### Let the site owner choose

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. In the admin, open **Theme > Colours and fonts** (`<admin>/theme/colours`). The owner sets the brand, background and text colours, the body and heading fonts and the three corner sizes here, then chooses **Save colours and fonts**.

2. Change **Brand colour** and save. Reload your site: the buttons use the owner's colour, not yours. Hatamoto prints the owner's values in a small `<style>` block after your stylesheet, so they win. It prints only the values the owner changed from the stock defaults, so your theme's defaults hold for the rest.

### Add a setting of your own

1. Give the theme a setting by adding a `settings_schema` to `theme.json`. It is a JSON Schema: a JSON description of the fields and their types.

   ```json
   {
     "name": "my-theme",
     "version": "0.1.0",
     "description": "My first Hatamoto theme.",
     "settings_schema": {
       "type": "object",
       "properties": {
         "banner_colour": {
           "type": "string",
           "title": "Banner colour",
           "default": "#fdf2e9"
         }
       }
     }
   }
   ```

2. Read the setting in `layouts/home.html` with `.Settings`:

   ```html
   {{define "content"}}
   <div class="my-banner" style="background: {{.Settings.banner_colour}}">
     <p>Welcome to {{.Site.Name}}</p>
   </div>
   {{template "page-body" .}}
   {{end}}
   ```

   Remove the `background` line from `.my-banner` in your CSS, so the setting decides the colour. Save both files.

3. Open **Theme > Themes** and choose **Settings** on the `my-theme` card. The form shows a **Banner colour** field, built from your schema. Enter `#e9f5fd` and choose **Save settings**. Hatamoto confirms with "Settings saved for my-theme."

Settings are stored per theme and kept when you install a newer version. A setting with a `default` always has a value, even before the owner saves the form. Section templates read the same values as `{{.Settings.banner_colour}}`.

## Check it worked

- Your site's buttons and headings use your CSS values, until the owner picks other values on **Theme > Colours and fonts**.
- The banner on the homepage has the colour from the theme's **Settings** form. View the page source: the banner reads `style="background: #e9f5fd"`.

If the owner's colours do not show, your theme probably ships its own `layouts/base.html` without the token style block. Copy that block from the built-in `base.html`, or read the values from `.Tokens` yourself; [Builder contract](builder-contract.md) has the details and the larger token set for the visual editor.

If the **Settings** button is missing on the card, the push did not include the new `theme.json`: look for a `NOT PUSHED` line in the terminal. Values print escaped, so a value that is not a plain colour, such as one with a semicolon, shows up in the page as `ZgotmplZ` instead of CSS. Use plain values such as `#e9f5fd`.

For how the stylesheet is built and cached, see [Assets, icons and feeds](assets-icons-feeds.md).
