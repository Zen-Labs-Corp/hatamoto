# Single sign-on (OpenID Connect)

Owners can let people sign in to the admin with an identity service they already use: Microsoft Entra ID,
Okta, Google Workspace or any other OpenID Connect issuer. It is off by default and costs nothing while off:
the login page, its routes and its cookies stay exactly as they were.

Turn it on in **Settings > Users > Sign-in**, in the card **Single sign-on (OIDC)**. Only owners see it, and
saving asks for the sudo re-confirmation.

## What it does

- A **Continue with <name>** button appears on the login page. The label is yours to set.
- Pressing it sends the person to the identity service; they come back to the **redirect URL** shown in the
  card. Give that URL to the identity service when you register Hatamoto there. It is built from the site
  address (`PUBLIC_BASE_URL`, else the Site address setting) under the admin path. Without a site address the
  card asks you to set one first.
- Hatamoto then checks the answer and signs the person in with the same session code as a password or a
  passkey. Access end dates, lockouts and the sign-in policy apply as usual.
- Every outcome is written to the audit log: signed in, signed in and asked for the second step, refused
  (with a short reason), account created, identity linked.

## The rules you set

| Setting | Meaning |
|---|---|
| Issuer | The identity service's address (https, no query or fragment). Hatamoto reads its discovery document at `<issuer>/.well-known/openid-configuration`. |
| Client ID and secret | From the app you register at the identity service. The secret is stored sealed with `HATAMOTO_SECRET`, is never shown again, and a blank field keeps the saved one. If `HATAMOTO_SECRET` changed, the card says the secret is lost and asks for it again. |
| Allowed domains | The part of the email after the last `@` must match one of these exactly. At least one is required. |
| Default role | The role of an account created on first sign-in. |
| Link existing accounts | Off by default. When on, a person whose verified email matches an existing account in an allowed domain is linked to it. When off, such a person is refused. Never turn it on for an identity service that lets people choose any email address. |

A person is created on first sign-in only when the identity service says the email is verified (the
`email_verified` claim, or `xms_edov` for Entra ID) and the domain is allowed. After that, the account is found by
the pair issuer and subject, not by email, so a changed email at the identity service does not matter. **Changing
the issuer means everyone is a new identity**: with the link switch on, existing accounts are linked again by
email on their next sign-in; with it off, they are refused.

## Microsoft Entra ID

1. In the Entra admin center, open **App registrations** and choose **New registration**. Pick the account
   types that fit, and add a **Web** redirect URI: the URL from the card.
2. Under **Certificates & secrets**, create a client secret and copy its value.
3. Issuer: `https://login.microsoftonline.com/<tenant-id>/v2.0` (the tenant ID is on the app's overview page).
4. Client ID: the **Application (client) ID**.
5. Entra includes the email only when the account has one. For a verified email, either add the optional claim
   `xms_edov` to the ID token, or `email_verified`. Keep the response mode at its default (query).

## Okta

1. In the Okta admin console, create an **App integration**: OIDC, **Web application**.
2. Set the **Sign-in redirect URI** to the URL from the card, and assign the people who may use it.
3. Issuer: `https://<org>.okta.com`, or the URL of your custom authorization server.
4. Client ID and secret: from the app's **General** tab.

## Google Workspace

1. In the Google Cloud console, create an OAuth client of type **Web application**, with the URL from the card
   as an authorised redirect URI.
2. Issuer: `https://accounts.google.com`.
3. Restrict who can sign in with **Allowed domains**. Google's `hd` claim is not trusted; only the email's domain
   is checked.

## Test sign-in

**Test sign-in** in the card fetches the saved issuer's discovery document and signing keys and reports what
failed. It sends no secret and signs no one in. It does not prove that the client ID, secret or redirect URL are
right; sign in once in a private window after saving.

## The second step

A person who has a passkey or an authenticator on their Hatamoto account is still asked for it after single
sign-on. The identity service's own multi-factor result (`amr`) is not trusted in this version.

## Several servers

Settings are read from the database and cached for up to 15 seconds, so a change reaches every server within
15 seconds. The server where you save applies it at once.

## Rolling back

People created by single sign-on have no password. Before rolling back to a release without this feature, give
them one (`hatamoto user reset-password`) or remove them; see [ADR 0010](adr/0010-sign-in-providers.md).
Password sign-in is never switched off by this feature, so a wrong issuer cannot lock an owner out.

## Limits

- One OpenID Connect issuer at a time.
- An identity service on a private network (loopback or private addresses) is not supported; discovery, keys and
  token calls refuse such addresses. There is no switch for it.
- No SAML, SCIM or group-to-role mapping, and no way to require single sign-on for everyone.

See also [Outbound connections](site/operate/outbound-connections.md) and the
[security model](SECURITY_MODEL.md).
