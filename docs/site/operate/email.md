---
title: Set up email
nav_title: Email
description: Connect Hatamoto to an SMTP server so password resets, invitations and form notifications reach people, then send a test message.
next: configuration-reference.md, ../admin/users-and-api-keys.md
---

In this guide you connect Hatamoto to an SMTP server and send a test message. Hatamoto sends a small amount of mail: password resets, user invitations, form notifications and form confirmations. An owner sets email up in the admin, at **Settings > Email**. You can also supply it in the server's environment instead; settings saved in the admin win.

## Before you start

- An account with the owner role, the role that can change settings, in the admin.
- An SMTP account and its user name and password. Note which port and security the provider asks for: usually port 587 with STARTTLS, or 465 with TLS (SSL).
- A sender address on your own domain, with SPF, DKIM and DMARC records for that provider.
- `PUBLIC_BASE_URL` set, or the Site address setting filled in, because reset and invitation links are only mailed when one of them is set.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Sign in as an owner and open **Settings > Email** (`<admin>/settings/email`). The first-run wizard shows the same form, so it may be filled in already.

2. Choose a **Provider**. **Amazon SES** and **Gmail / Google Workspace** fill in the host for you; for SES, pick the **Amazon SES region** (it starts as `us-east-1`). For any other provider choose **Custom** and enter its **Host**. Use the provider's host name, not an IP address, because the certificate is checked against that name.

3. Set **Port** and **Security** to what the provider asks for. **STARTTLS** usually goes with port 587, and **TLS (SSL)** with port 465. **None** sends without encryption and is accepted only for `localhost`, `127.0.0.1` or `::1`, such as a mail catcher on the same machine.

4. Enter the **Username** and **Password**, or leave both blank if the server needs no sign-in. If you later change the host or the user name, enter the password again: a blank password keeps the saved one only while those two stay the same.

5. Enter the **From address** and, optionally, a **From name**. A blank From name uses your site name, and follows it when you rename the site.

6. Select **Save**. Hatamoto first checks that it can connect and sign in, without sending mail. If a step fails, the page shows the server's answer and nothing is saved. It applies at once.

7. Select **Send a test email** to send a message to your own address.

### Use the environment instead

To keep mail settings in the server's environment, leave **Settings > Email** empty and add the `SMTP_*` variables: under systemd in `/opt/hatamoto/config/hatamoto.env`, under Docker in the `environment:` block of the `hatamoto` service, written as `KEY: value`. These variables always use STARTTLS, so a port such as 465 needs the admin form instead. Quote a value that has spaces, as below: the file is also loaded by bash for the `hatamoto` commands, and systemd removes the quotes.

```ini
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=hatamoto
SMTP_PASSWORD=app-specific-password
SMTP_FROM=noreply@example.com
SMTP_FROM_NAME="Example Site"
```

Set `SMTP_FROM` whenever `SMTP_HOST` is a real host: Hatamoto refuses to start with the default `noreply@localhost`. Then restart the service (`sudo systemctl restart hatamoto-api`), or under Docker run `docker compose -f docker-compose.yml -f production.yml up -d`, which recreates the container with the new environment.

Settings saved in the admin replace these variables as a whole, not field by field. **Use the server's SMTP_* settings instead** on **Settings > Email** deletes the saved settings and goes back to the environment.

While email is off, Hatamoto still works: owners get one-time links to pass on, and form submissions are kept in the admin without a notification. The full list of mails, these fallbacks and the rules for several processes are in the [configuration reference](configuration-reference.md#email-delivery).

## Check it worked

**Settings > Email** shows **Email is set up**, and a **Using:** line says where the settings come from: "settings saved here" or "the server's SMTP_* settings". The test says `Test email sent to <your address>. Check your inbox.`, and the message arrives from the name and address you set. A second check: sign out, use **Forgot your password?** on the sign-in page with your own address, and follow the link in the mail within one hour.

If the page asks you to "Re-enter your SMTP password", the server secret has changed: enter it again. Until then Hatamoto uses the server's `SMTP_*` settings.

If the test says `The test email could not be sent`, or nothing arrives:

1. Search the log for `failed to dial SMTP server` (the host or port cannot be reached), `failed to start TLS` (the server does not offer it, or its certificate does not match the host) or an authentication error.
2. Check that the server reaches the provider's port:

   ```bash
   openssl s_client -starttls smtp -connect smtp.example.com:587
   ```

3. Check that the sender's domain has SPF, DKIM and DMARC records for the provider.
4. Make sure `EMAIL_DISABLED` is not `true`. It switches off every send, even with saved settings; the log shows `email disabled by EMAIL_DISABLED env var, dropping send`. **Settings > Email** warns that email is switched off, but invitations and forms still report success.

If a user is locked out while you fix email, set a password from the shell on the server, as the service user:

```bash
hatamoto user reset-password --email you@example.com
```

It prints a temporary password (unless you pass one with `--password-stdin`), unlocks the account and signs the user out everywhere. See the [command-line reference](cli-reference.md).
