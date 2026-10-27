---
title: Build your first theme
nav_title: Your first theme
description: Make a small theme with one template and one stylesheet, push it to a development site and watch your changes go live.
next: overview.md, change-colours-and-fonts.md, add-a-section.md
---

In this guide you make a theme of three files, push it to a development copy of your site and change it while it runs. At the end your homepage shows a welcome banner that you wrote, and every save you make appears on the site within a second or two.

You do not have to write a whole theme. Hatamoto ships a built-in theme called `default`, and any file your theme leaves out comes from it. So a theme can start with one template.

## Before you start

- A Hatamoto site you use for development, running on your computer or a test server. Its `ENVIRONMENT` setting must not be `production`: Hatamoto refuses development themes on a production site.
- A terminal on the same machine, with the `hatamoto` program on your path.
- The **database address**, the `DATABASE_URL` value that tells Hatamoto where its Postgres database is. The push command writes your theme into that database, and the running site picks it up from there.
- A text editor.

## Steps

### Create the files

1. Make a folder for the theme, with two folders inside it:

   ```bash
   mkdir -p my-theme/layouts my-theme/assets/css
   ```

2. Create `my-theme/theme.json`. This file is the theme's **manifest**, the file that gives its name and version:

   ```json
   {
     "name": "my-theme",
     "version": "0.1.0",
     "description": "My first Hatamoto theme."
   }
   ```

   The name starts with a lower-case letter, followed by 1 to 39 lower-case letters, digits or hyphens. The name `default` is taken by the built-in theme. The version uses the usual `major.minor.patch` form.

3. Create `my-theme/layouts/home.html`. A **layout** is the template for a whole page, and `home.html` is the one Hatamoto uses for the homepage:

   ```html
   {{define "content"}}
   <div class="my-banner">
     <p>Welcome to {{.Site.Name}}</p>
   </div>
   {{template "page-body" .}}
   {{end}}
   ```

   The templates use Go's template language: `{{ }}` marks the parts the server fills in. Every layout defines a block named `content`, which the page frame (header, footer and the HTML head) wraps. `{{.Site.Name}}` prints your site name, and `{{template "page-body" .}}` prints the page's own content, using a **partial** (a smaller template shared between layouts) from the built-in theme.

4. Create `my-theme/assets/css/my-theme.css`:

   ```css
   .my-banner {
     padding: 2rem;
     text-align: center;
     background: #fdf2e9;
     font-size: 1.5rem;
   }
   ```

   Hatamoto joins every file in `assets/css/` into one stylesheet and links it on every page, so you do not add a `<link>` tag yourself.

### Push the theme

1. In the terminal, set the database address of your development site. Replace the value with your own:

   ```bash
   export DATABASE_URL=postgres://hatamoto:hatamoto@localhost:5432/hatamoto
   ```

2. Start the push command from the folder that holds `my-theme`:

   ```bash
   hatamoto theme dev ./my-theme --dev
   ```

   The `--dev` flag confirms that this is a development site. The command packs the folder, checks it, installs it and makes it the live theme:

   ```text
   watching ./my-theme; Ctrl-C to stop
   20:17:39  pushed my-theme v0.1.0 (dev, unsigned)
   ```

3. Open your site's homepage in a browser. The banner shows above the page content.

### Change it while it runs

1. Leave the command running. In `layouts/home.html`, change `Welcome to` to `Hello from` and save. The command notices the change within half a second and pushes again:

   ```text
   20:17:42  pushed my-theme v0.1.0 (dev, unsigned)
   ```

2. Reload the homepage. It shows the new text, with no restart.

3. Press Ctrl-C when you are done. The theme stays installed and live.

## Check it worked

The homepage shows your banner, and this command lists your theme as live:

```bash
hatamoto package list
```

```text
theme   my-theme   0.1.0   dev, active, unsigned
```

Other pages, such as a blog post, still look as before, because your theme has no layout for them and the built-in theme supplies one.

If a save prints a line such as `NOT PUSHED: package: the theme does not render: ...`, the theme has an error. The message names the file and the problem. Fix it and save again; until then the site keeps the last version that worked.

If the command stops with `theme dev is for development: dev packages need an explicit --dev flag`, add `--dev` or set `ENVIRONMENT=development` in your shell. If it says the database belongs to a production server, point `DATABASE_URL` at a development copy instead.

To go back to the built-in theme, run `hatamoto theme activate default`. When the theme is ready for a real site, follow [Package and share a theme](packaging.md).
