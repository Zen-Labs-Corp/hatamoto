---
title: Package and share a theme
nav_title: Package and share
description: Turn your theme folder into one signed file, install it on your real site, update it later and remove it when you no longer need it.
next: ../plugins/package-and-install.md, package-format.md
---

In this guide you turn your theme folder into one signed file, set up your site to trust it, and install it. At the end the theme runs on your real site, and you know how to ship an update and how to remove it.

A **package** is one `.hmp` file that holds a whole theme. **Signing** proves who made the package and that nobody changed it since. Your site installs only packages signed by a **trusted key**, a public key it accepts signatures from.

## Before you start

- A theme folder that works on your development site, such as `my-theme` from [Build your first theme](your-first-theme.md).
- The `hatamoto` program on your computer. Making keys and packages needs no database.
- Access to your real site: an owner account, and for the trust step a shell on the server where you can change the service's settings.

## Steps

### Create a signing key

You do this once, and use the same key for every package you make.

1. Run this on your computer, in a folder outside your theme:

   ```bash
   hatamoto keys generate --name mykey
   ```

   ```text
   key id:      303d64f631f1fdf8
   secret key:  mykey.key (keep it private; it signs your packages)
   public key:  mykey.pub

   To trust packages signed with it, set on the server:
     HATAMOTO_TRUSTED_KEYS=MOuTzD6+6nTzsdLuBxOidvTWG8tYB3k4GG8e+S0A6oo=
   ```

   Keep `mykey.key` private and out of version control. Anyone with it can sign packages in your name. The command never overwrites an existing key file.

### Pack the theme

1. Set the version in `my-theme/theme.json`, for example `"version": "1.0.0"`. Each new release needs a higher version.

2. Pack and sign the folder:

   ```bash
   hatamoto theme pack ./my-theme --key mykey.key
   ```

   ```text
   my-theme-1.0.0.hmp
     theme my-theme v1.0.0, 2246 bytes
     sha256 ad15cb12d0d2bdbe14d26a9af0f4d7b10c5bb0921b25984419598e2e6ad3f205
     signed by 303d64f631f1fdf8
   ```

   The file name is the theme name and version. The command checks the package before it writes it, so a file it builds can be installed.

### Trust the key and install

1. On the server, add the `HATAMOTO_TRUSTED_KEYS` line that `keys generate` printed to the settings of the Hatamoto service, then restart it. To trust several keys, separate them with commas or spaces.

2. Install the package. In the admin, open **Theme > Themes**, choose **Upload theme**, pick `my-theme-1.0.0.hmp` and choose **Upload**. Or, on the server, with `DATABASE_URL` and `HATAMOTO_TRUSTED_KEYS` set in your shell:

   ```bash
   hatamoto package install my-theme-1.0.0.hmp
   ```

   ```text
   installed theme my-theme v1.0.0
   activate it in Admin > Themes
   a running server applies it within a few seconds; no restart is needed
   ```

   Installing does not change what visitors see.

3. Choose **Preview** on the theme's card to check it over your real content, then **Activate**. [Preview and develop a theme](preview-and-dev.md) covers the preview.

### Ship an update

1. Raise the version in `theme.json`, pack again and install the new file the same way. It replaces the installed version and keeps the theme's settings.

   Hatamoto asks you to confirm when the new file is not a newer version, when a different key signed it, or when the theme is live and would drop section types your content uses. From the terminal, add `--confirm` to `package install` to go ahead.

### Remove a theme

1. To remove a theme, choose **Uninstall** on its card. This deletes the package and its settings and nothing else: pages, posts, media, menus and site settings stay. You cannot remove the live theme, so activate another one, or the built-in `default`, first.

## Check it worked

On the server, list what is installed:

```bash
hatamoto package list
```

```text
theme   my-theme   1.0.0   upload, active, signed 303d64f631f1fdf8
```

The card on **Theme > Themes** shows **Signed by** with your key id, and the site shows your theme.

If installing says `package is signed by a key you do not trust`, the key is missing from `HATAMOTO_TRUSTED_KEYS` where you ran the command. If the install worked but the site still shows the old theme, and the server log says `installed package not loaded` with `no longer in HATAMOTO_TRUSTED_KEYS`, the running server does not have the key: add it to the service's settings and restart.

To publish themes for other sites, run your own registry, a place other sites browse and install packages from. The package rules, the trust checks and the registry are described in [Package format and commands](package-format.md).
