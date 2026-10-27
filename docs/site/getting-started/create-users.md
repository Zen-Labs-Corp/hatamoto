---
title: Create users
nav_title: Create users
description: Add owner and editor accounts, and reset a forgotten password, from the server's terminal.
next: ../admin/tour.md, ../admin/users-and-api-keys.md
---

In this guide you add an account for a colleague and reset a password, with one command each on the server. This works without email and without the admin, which makes it the way to rescue an owner who is locked out. At the end your colleague can sign in with a temporary password.

## Before you start

- A Hatamoto site where the first-run wizard has run, from [First run](first-run.md).
- A terminal on the server that runs Hatamoto, as the same system user that runs it.
- The **database address**, the `DATABASE_URL` setting that tells Hatamoto where its Postgres database is. The commands talk to the database directly, so the server does not need to be running.
- Your colleague's email address.

Each account has a **role**, which decides what it may do:

| Role | Can do |
|---|---|
| `owner` | Everything, including settings, users, packages and updates. |
| `editor` | Content work: pages, posts, media and similar. |

There is no viewer or author role. A new account is an `owner` unless you say otherwise.

## Steps

### Add an account

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Open a terminal where Hatamoto's settings are loaded, so `DATABASE_URL` is set:

   - The program file: the same terminal and folder you started `./hatamoto serve` from, with `DATABASE_URL` set as in [Install the binary](install-binary.md).
   - Docker: the folder with your `docker-compose.yml` file, the file that describes your Docker setup. Put `docker compose exec hatamoto` in front of each `hatamoto` command below, for example `docker compose exec hatamoto hatamoto user create --email sam@example.com`.
   - A service managed by **systemd**, the service manager on most Linux servers: follow the command pattern in [Deploy with systemd](../operate/deploy-systemd.md), which loads the service's settings first.

2. Create the account. This example makes an editor named Sam:

   ```bash
   hatamoto user create --email sam@example.com --name "Sam Editor" --role editor
   ```

   With the program file in the current folder, type `./hatamoto` instead of `hatamoto`. The command prints the account and a new random password, once:

   ```text
   created editor user sam@example.com
   temporary password: <24 random characters>
   ```

3. Send the password to Sam through a private channel. Hatamoto stores only a scrambled form of it and never writes it to its log, so you cannot read it again. Clear your terminal history if others use the same terminal.

4. Ask Sam to sign in at `<admin>/login` on your site, and change the password in the admin.

To set the password yourself instead of getting a random one, pass it on the first line of standard input. It must be at least 12 characters:

```bash
printf '%s\n' "$NEW_PASSWORD" | hatamoto user create --email you@example.com --password-stdin
```

There is also a `--password` option, but the password then shows in the server's process list and your shell history, so avoid it. Every option is listed in the [CLI reference](../operate/cli-reference.md).

### Reset a password

1. Reset the password of an account that forgot it or is locked out:

   ```bash
   hatamoto user reset-password --email sam@example.com
   ```

   ```text
   password reset for sam@example.com
   temporary password: <24 random characters>
   ```

   This does three things at once:

   - It sets the new password, random unless you give one with `--password-stdin`.
   - It unlocks the account. Five failed sign-ins in a row lock an account for 15 minutes.
   - It signs the account out on every device.

Day to day, owners can also add people in the admin, at **Settings > Users** (`<admin>/settings/users`), and send them an invitation by email. Invitations and password reset by email work only once email is set up, by an owner at **Settings > Email** or by the server's `SMTP_*` settings; see [Users and API keys](../admin/users-and-api-keys.md) and [Email](../operate/email.md). Without email, the **Forgot your password?** link on the sign-in page leads to a page that shows the reset command above, which is why the command exists.

## Check it worked

Sam opens `<admin>/login` on your site, signs in with the email address and the temporary password, and lands in the admin. An editor sees content screens, but not **Settings > Users**.

If the command does not work, its last line says why:

- `email already exists`: the account is already there. Reset its password instead.
- `no user with email sam@example.com`: no account has that address. Check the spelling, or create it.
- `invalid role`: the role must be `owner` or `editor`.
- `password must be at least 12 characters`: choose a longer password, or leave it out to get a random one.
- `DATABASE_URL is required`: the terminal does not have Hatamoto's settings loaded. Go back to step 1.
- `password changed but revoking sessions failed`: the new password is set, but the account was not signed out everywhere. Run the reset again.

Keep at least one owner. The admin refuses a change that would remove or demote the last owner.
