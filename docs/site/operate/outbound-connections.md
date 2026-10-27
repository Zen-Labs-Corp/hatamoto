---
title: Outbound connections
nav_title: Outbound connections
description: Every network call a Hatamoto server makes on its own or on your behalf, what it sends, when it runs and how to turn it off.
---

This page lists every outbound connection of the `hatamoto` server, so you can decide what to allow on a locked-down network. Hatamoto sends no telemetry and no usage reports. To close the optional ones on a production server, work through [Harden an instance](hardening.md).

## Overview

| Connection | To | When | What it sends | Turn it off |
|---|---|---|---|---|
| Update check | `https://hatamoto.dev/releases/channels/<channel>.json` | About every six hours (give or take 30 minutes), first within ten minutes of start | One anonymous GET. No query, cookie or identifier of your install; only the headers Go adds itself. | `HATAMOTO_UPDATES=off`, or the update mode in Settings > Updates ([policy](../updates/pin-or-turn-off-updates.md)) |
| Update download | The same host: the release record and the binary | Only when the check finds a release the policy installs, or you press Update now | Anonymous GETs, same as above | `HATAMOTO_UPDATES=notify` (look, never install) or `off` |
| Registry | `https://hatamoto.dev/registry.json`, then the package files it lists | Only when an admin opens the Plugins or Themes browser, or installs from it. The public directory page also fetches it when one is served | Anonymous GET | `HATAMOTO_REGISTRY_URL=off` (uploads still work) |
| AI-crawler IP ranges | The published range files of the crawler operators in the built-in agent registry: Google, Bing, OpenAI, Perplexity, Anthropic, Common Crawl, DuckDuckGo, Apple and Ahrefs | First about 90 seconds after start, then daily | GET with the user agent `hatamoto-agent-verifier`; no cookies, nothing about your site | `HATAMOTO_AGENT_RANGES=off` |
| Reverse DNS | Your system's DNS resolver | When a hit claims to be a crawler whose operator is verified by host name, or whose ranges are not downloaded yet (checked by the worker, every five minutes) | A reverse lookup of the visitor's IP address, then a forward lookup of the name it returns | `WORKER_DISABLE_JOBS=agent_verify` (hits stay unverified) |
| Captcha | The provider you pick: Cloudflare Turnstile, Google reCAPTCHA or hCaptcha | When a visitor submits a form that has the captcha switched on | Your secret key, the visitor's captcha token and the visitor's IP address | Do not enable a captcha on the form |
| Email | The SMTP host you configure | When Hatamoto sends a password reset, an invitation or a form notification, or an owner presses **Send a test email** (**Test connection** on the captcha settings also reaches the provider) | The message | Leave email unconfigured: no saved Settings > Email (**Use the server's SMTP_* settings instead** removes it) and no `SMTP_HOST` |
| Webhooks | The URLs you subscribe in Settings > Webhooks | On the events you pick | The event payload, signed | Delete the subscription |
| Plugins | The hosts a plugin declares in `allowed_hosts` and you granted | When the plugin calls `http.fetch` | Whatever the plugin sends | Do not grant `http.fetch`; `HATAMOTO_PLUGIN_FETCH_PER_MINUTE` caps the rate |
| Embeds in pages | The visitor's browser, not your server, loads YouTube, Vimeo or map iframes | When a visitor opens a page that contains one | Nothing from the server | Remove the embed; `HATAMOTO_EMBED_HOSTS` limits which hosts rich text may embed |
| WordPress import | The WordPress site you import from, every media URL it (or the WXR file) lists, and the `--audit-url` site | Only during an import you start | Requests for the content and the media files | Do not run an import |

Plugin calls, webhooks and the import are checked against private, loopback and link-local addresses before they connect, so they cannot be pointed at your internal network (the command-line import can lift this with `--allow-private-hosts`, and `--audit-url` may be local).

## Where each one lives in the code

| Connection | Code |
|---|---|
| Update check, schedule | `backend/internal/updater/run.go:43` (`checkEvery`, `checkJitter`), first run at `:137` |
| Update fetches | `backend/internal/updater/fetch.go:23` (default host), `:45` (no headers of its own), `:120` (manifest), `:128` (record), `:147` (download) |
| `HATAMOTO_UPDATES` | `backend/internal/updater/types.go:31` and `:84` |
| Registry | `backend/internal/config/packages.go:13` (default URL), `:24` (`off`); `backend/internal/packages/registry.go:154`; `backend/internal/server/admin_packages.go:61`; `backend/internal/server/registry_directory.go:148` |
| IP ranges | `backend/internal/agents/registry.json` (hosts); `backend/internal/agents/refresh.go:14` and `:108` (user agent); `backend/internal/worker/scheduler.go:343` (daily, 90 s after start); `backend/internal/server/worker_jobs.go:18` and `:47` (`HATAMOTO_AGENT_RANGES=off`) |
| Reverse DNS | `backend/internal/agents/verify.go:128` (`LookupAddr`); `backend/internal/agents/verify_worker.go` |
| Captcha | `backend/internal/spam/verify.go:28` (endpoints), `:85` (fields sent) |
| Email | `backend/internal/email/`, `backend/internal/mailsettings/settings.go` |
| Webhooks | `backend/internal/webhook/outbound.go`, `deliver.go` |
| Plugin `http.fetch` | `backend/internal/pluginhost/fetch.go:20` |
| Embeds | `backend/internal/render/funcs.go:128`; `backend/internal/entity/sanitize.go:19` |

## Related

- [How updates work](../updates/index.md) and [pin or turn off updates](../updates/pin-or-turn-off-updates.md)
- [Configuration reference](configuration-reference.md)
- [Harden an instance](hardening.md)
