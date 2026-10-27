---
title: Package and install a plugin
nav_title: Package and install
description: Turn your plugin into one signed file, install it on a site, update it to a new version and remove it again.
next: packaging-signing.md, manifest.md
---

So far your plugins ran from a development folder. In this guide you turn the `counter` plugin into one signed file and install it the way a real site does. You also update it and remove it, so you know what happens to its data.

## Before you start

- The `counter` plugin from [Add an admin page](add-an-admin-page.md), built into its `build` directory.
- The `hatamoto` program on your machine, and a site you can sign in to as an owner. To install from the command line you also need a shell on the server with its `DATABASE_URL` set.

A **package** is one `.hmp` file that holds a whole plugin. **Signing** proves who made it and that nobody changed it. A **trusted key** is a public key your server accepts signatures from. A server refuses a package signed by a key it does not trust.

## Steps

### Sign and pack

1. Make a signing key, once, on your own machine:

   ```bash
   hatamoto keys generate --name mykey
   ```

   ```text
   key id:      359345ada766528b
   secret key:  mykey.key (keep it private; it signs your packages)
   public key:  mykey.pub

   To trust packages signed with it, set on the server:
     HATAMOTO_TRUSTED_KEYS=NDFsJlz8HdPiLwxtPTxgioR+oX/KQPmy0WFvjqfWD7w=
   ```

   Your key id and value differ. Keep `mykey.key` out of version control. The command refuses to overwrite an existing key.

2. From the `counter` directory, pack the `build` directory and sign it:

   ```bash
   hatamoto plugin pack build --key mykey.key
   ```

   ```text
   counter-0.2.0.hmp
     plugin counter v0.2.0, 2082953 bytes
     sha256 26f3caff7271771aa5c8874be6623ac6dbf8c7eab694a6c5b8792b58adda854c
     signed by 359345ada766528b
   ```

   The directory must hold `plugin.json` and `plugin.wasm`. The command checks the manifest (`plugin.json`) and the file layout, so a package it writes can be installed.

### Trust and install

1. On the server, add the line that `keys generate` printed to the service's environment, and restart Hatamoto. Separate several keys with commas or spaces.

2. Remove `counter` from your development plugin directory, if it is still there, and restart. A plugin in `HATAMOTO_PLUGIN_DIR` replaces an installed package of the same name.

3. In the admin, open **Plugins** and click **Upload package**. Choose `counter-0.2.0.hmp` and click **Upload**.

4. The install screen says who signed the package and lists the **permissions** it asks for, the rights it may use on your site, all ticked. Untick anything you do not want, then click **Install plugin**. The plugin is live at once, with no restart.

5. Or, from a shell on the server, install with one command instead of steps 3 and 4. `--grant` names the permissions to give it:

   ```bash
   hatamoto package install counter-0.2.0.hmp --grant kv,settings
   ```

   ```text
   installed plugin counter v0.2.0
   a running server applies it within a few seconds; no restart is needed
   ```

### Update and remove

1. To update, raise `version` in `plugin.json` (for example to `0.3.0`), build, pack and upload the new file. The install screen says it replaces the old version. Earlier grants stay ticked, and any permission the new version adds is marked "(new in this version)" and left unticked.

2. To remove the plugin, open **Plugins > counter > Permissions** and click **Uninstall**. Choose **Keep its data.** to keep the stored count for a later install, or **Delete its data too.** to remove it for good. Your pages, posts, media and site settings are never touched.

## Check it worked

1. **Plugins** lists `counter` with the badge **Active**, and its page shows the version you installed.
2. `curl <site>/p/counter/count` answers with the count, as before.
3. After an uninstall, **Plugins** no longer lists `counter`, and `/p/counter/count` answers `404`.

If the upload says "package is signed by a key you do not trust", the server does not have your key yet: check `HATAMOTO_TRUSTED_KEYS` and restart. If it says the package "is not signed", you packed without `--key`. Always sign; the switch that allows unsigned packages is meant for code you wrote and reviewed yourself.

The package layout, the size limits, key rotation, registries and every related setting are on [Packaging and signing](packaging-signing.md).
