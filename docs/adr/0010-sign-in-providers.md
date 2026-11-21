# ADR 0010: Sign-in providers, provisioning and sign-in policy

Status: accepted (2026-11-21)

## The rules in plain words

1. A compiled-in module can add sign-in providers and one sign-in policy
   through `hatamoto.dev/core/auth`. It registers them from an `init`
   function; `app.Main` freezes the registry before anything else runs.
2. With nothing registered, sign-in is exactly what it was: same login page
   bytes, same routes, same cookies.
3. Every provider sign-in ends in the same session code as a password or
   passkey sign-in. The access end date, the lockout and the sign-in policy
   apply to it. The module cannot open a session itself.
4. Anything unexpected fails closed and shows the neutral message, never the
   provider's own error text: a refusal, an error, a bad state value, an
   identity that is not linked to the user the module returned.
5. Core never links an identity to an existing account on its own. The module
   asks for it, and may do so for an existing account only on a verified email
   in a domain the site owner has allowed.

   Core does not check this rule: it only requires that the identity ends up
   linked to the user the module returned. A module must never link an
   identity to, or find or create a user by, an email address unless the
   provider marks it verified (`Identity.EmailVerified`) and the issuer is one
   it trusts. An unverified address lets anyone who registers at the provider
   with another person's email take over that person's account.
6. Core implements user provisioning. A module calls it and does not replace
   it.

## Context

A site may want people to sign in through an outside identity service, and
may want its own rule about who may sign in at all. Both need to touch the
login flow, the user list and the session code, which are internal to core and
cannot be imported from another module. This ADR publishes a small, frozen
interface for them. It follows the registration rules of
[ADR 0007](0007-module-extension-points.md): register in `init`, freeze in
`app.Main`, panic on a duplicate, a nil or a late registration, and a
`Reset` hook for tests only.

## Decision

### 1. Providers

`auth.RegisterProvider(id, name, provider)` registers one provider. The id
matches `^[a-z][a-z0-9-]{0,31}$` so it is safe in a path; the name is 1 to 64
characters. Providers are listed in registration order.

A provider implements three methods:

- `Start(ctx, StartRequest) (StartResult, error)` returns the address to send
  the browser to and an opaque `Data` string (at most 2048 bytes) that comes
  back to `Finish`.
- `Finish(ctx, FinishRequest) (Identity, error)` turns the callback into an
  `Identity`: issuer, subject, email, whether the email is verified, a name,
  and whether the provider itself performed a second factor (`MFA`).
- `MapUser(ctx, MapRequest) (User, error)` decides which user the identity
  is. It gets the user already linked to the identity (or none) and the
  `Provisioner`.

The login page shows each provider as a "Continue with <name>" link. The
routes are `<admin>/login/provider/<id>/start` and
`<admin>/login/provider/<id>/callback`. Both are GET, both sit behind the
login rate limit, and both are reachable signed out, also on a custom admin
address. The callback address is built from the configured site address; with
none set the sign-in fails with the neutral message and a log line.

Core owns the state. It makes a random state value and seals it, with the
provider id, the `Data`, a next-page value and an expiry, in a cookie
(`hm_prov`) encrypted with a key derived from the site secret. The cookie is
`HttpOnly`, `SameSite=Lax` (a Strict cookie is not sent on the redirect back
from another site), limited to the provider's path, `Secure` on HTTPS and valid
for ten minutes. It is single use: the callback always clears it and remembers
its id until it expires. A missing, forged, expired, replayed or mismatched
state is refused before `Finish` runs.

A POST callback is not supported. A provider whose protocol needs one is a
later extension of this interface.

### 2. Linking and the second step

After `MapUser`, core looks the identity up again and requires it to belong to
the user the module returned. A module that returns a user it did not link
gets a refusal and no session.

If `Identity.MFA` is false and the user has any second factor, core uses the
same two-step page as a password sign-in and opens the session only after the
factor is verified; the pending cookie records that the first step was a
provider. If `MFA` is true, or the user has no factor, the session is opened
at once. A site that requires two-factor for a user still enforces it on later
requests.

### 3. Sign-in policy

`auth.RegisterSignInPolicy(policy)` registers at most one. `AllowSignIn`
receives the user, the method (`password`, `passkey` or `provider`) and the
provider id. It must be free of side effects, because core may call it more
than once for one sign-in: early at the password check and again where the
session is issued. `false` or an error refuses the sign-in, and the user sees
the same message as for a wrong password.

It applies to the admin login (both password steps), the REST login, the
passkey sign-in, provider sign-in and the automatic sign-in after a password
reset, an invite or the first-owner setup. It does not apply to:

- command line `user reset-password`, `user create` and `user reset-2fa`,
  which work on the database directly and must keep working for an owner who
  is locked out;
- the password re-confirmation that guards a sensitive action, which confirms
  a session that already exists;
- API keys, which are not a sign-in method. Ending a user's access revokes
  their keys.

A provider sign-in also respects a stored lockout. Password and passkey
sign-ins keep the lockout behaviour they had.

### 4. Provisioning

`auth.Provisioning()` returns core's `Provisioner`, or `ErrNotAvailable` in a
process that does not serve (a command line run). It can find a user by id,
email (ignoring case and surrounding space) or identity; create a user
without a password; find or create by email; change name and role; set an
access end; disable; revoke sessions and keys; link and unlink identities; and
issue a password-set link.

- `Disable` sets the access end to now, then revokes every admin and API
  session and every API key the user created, in the same call. The last open
  owner cannot be disabled (`ErrLastOwner`) and nothing changes. Setting an
  access end that is not in the future revokes in the same way.
- Role changes and access ends go through the same owner guards as the admin.
- `IssuePasswordSetLink` returns a path below the site root
  (`<admin>/reset?token=...`) that lets the user choose a password. It shares
  the reset throttle (`ErrThrottled`).
- The provisioner lower-cases and trims the email of a user it creates, and
  finds users by email ignoring case.

### 5. Users without a password

A user created by the provisioner has no password (the column is NULL). Such a
user cannot sign in with a password: the attempt takes the same time as a wrong
password and shows the neutral message. It does not count toward the lockout,
because there is no password to guess and a lock would only keep the user out
of provider sign-in. A correct password that the sign-in policy refuses does
not count either, and does not clear the failure counter. They can
get one through a password-set link, a password reset or a passkey. The
re-confirmation that guards sensitive actions asks for a password, so a user
who has none needs one of those first.

### 6. Storage and portability

Linked identities live in `hatamoto_user_identities` (issuer and subject are
unique together; deleting the user deletes the rows). The migration only adds
a table that the previous release never reads. The data is another matter: a
user created without a password is stored with a NULL password hash, which the
previous release cannot read (it fails to load that user, so a password sign-in
answers with an error). Roll back only while no such user exists, or after each
of them has a password (`hatamoto user reset-password`).
Identities are not part of `hatamoto export`, like sessions and API keys; a
database backup carries them.

## Consequences

- A site with no module behaves as before; the registry costs one frozen
  lookup.
- A module can add a provider or a policy without importing `internal/`, and
  cannot weaken the lockout, the access end or the second step.
- The public package uses only standard-library types in its signatures, so
  the interface does not tie modules to core's internal packages.
- A provider that cannot work with a GET callback needs this ADR extended.
- A site that signs everyone in through a provider has users who cannot use
  the password re-confirmation until they have a password or a passkey.
