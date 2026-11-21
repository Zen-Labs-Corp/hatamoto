# ADR 0015: Plugin license and the edition plugin key

Status: accepted (2026-11-21).

## The rules in plain words

1. A plugin manifest may declare `license: "commercial"`. Empty is the default
   and means a normal plugin. A new install refuses any other value; a plugin already installed with one keeps loading as a normal plugin (older hosts ignored the member).
2. A host runs a commercial plugin only when its edition registered one plugin
   key with `edition.RegisterPluginKey`, the package is signed by that key and
   the edition's `PluginEntitled` says yes for the plugin name.
3. The plugin key is registered from an `init` function and frozen when
   `app.Main` starts. One key, one call; a second call or a late call panics.
4. The plugin key is never merged with `HATAMOTO_TRUSTED_KEYS`. Trusted keys do
   not sign commercial plugins and the plugin key does not sign anything else.
5. It fails closed. No key, no entitlement, a nil answer, a different signer, a
   dev source, a developer-index row, the unsigned opt-in or the plugin
   directory: the plugin is refused, at install and again at enable, load and
   every reconcile.
6. Core stays neutral. Core knows a manifest field and a key slot, nothing about
   who sells what or how entitlement is decided.

## Context

Core hosts plugins signed by trusted keys. A wrapper module (see
[ADR 0007](0007-module-extension-points.md)) that distributes paid plugins needs
core to enforce that such a plugin runs only where it was bought, without core
learning any product vocabulary. Trust in a signer says who wrote the package,
not whether this site may run it, so the two questions stay separate.

## Decision

`sdk.Manifest.License` (SDK 0.26.0) carries the declaration. The edition holds
one ed25519 public key and a `PluginEntitled(ctx, name)` answer. The package
manager treats a commercial plugin with its own policy that ignores every other
trust setting: signed by the plugin key, entitled, and an `sdk` range that
excludes hosts older than 0.26.

Entitlement is by plugin name, asked on each reconcile, so a gate caches it.
The CLI has no site id, so a gate that needs one denies there; commercial
plugins are installed from the admin.

An older host ignores the unknown field and would load the plugin as a free one.
The range rule closes that gap: a commercial plugin must declare `^0.26` or
later, and install refuses a range that admits an older host.

## Consequences

- Without a registered key the community binary refuses every commercial
  plugin and behaves as before for all others.
- The license field is a gate, not a sandbox or a payment mechanism: signing and
  entitlement decide, and the plugin still runs under the usual permissions.
- A key rotation is a new edition build; there is no list of plugin keys.
- Dropping `license` or changing its values is a breaking SDK change.
