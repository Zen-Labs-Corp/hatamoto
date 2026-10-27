# Admin static export (superseded)

> **Historical. Do not follow it.** This note assessed exporting the old Next.js admin as static files.
> That admin no longer exists: the admin is rendered by the Go binary (`html/template` + htmx) and
> Node is gone, as decided in [ADR 0002](adr/0002-single-binary.md) (2026-10-14; see also
> [`docs/ROADMAP.md`](ROADMAP.md)). Every file and line number below refers to the deleted `frontend/` tree.
> It is kept only because ADR 0002 cites it. It is not a description of the product.

**Status:** superseded (was: deferred). This change keeps Next.js serving `/admin` and `/auth`.
Go renders everything public. A Go-only `serve` therefore has no admin UI until the follow-up below lands.

Feasibility was assessed by static analysis only (nothing was built with `output:'export'`). Verdict: **feasible only with restructuring, not a flag flip.** The admin tree itself is nearly export-ready.

## Current config

`frontend/next.config.js` uses `output:'standalone'` (L3). Its `rewrites()` (L22-45) and `headers()` (L46-80) are unsupported under `output:'export'`. Middleware does not run either.

## Blockers

1. **Dynamic root layout.** `src/app/layout.tsx` L70-71 calls `headers()` and `cookies()` (locale for `<html lang>`), and L77 awaits `getPublicPluginBundles()`. That makes the whole app dynamic. It needs an admin-only root layout (route groups with separate root layouts) or removal of these calls.
2. **Public routes share the app.** `output:'export'` is app-wide, and these routes are dynamic:
   - `src/app/page.tsx`
   - `[locale]/*`: `layout.tsx` L26 `headers()`, `[...slug]/page.tsx` L355 `cookies()`
   - `(public)/layout.tsx` L43
   - `(public)/s/[...path]` L47/56/124
   - `(public)/post/[slug]` L75
   - `choose-region`
   - `robots.txt/route.ts`, the only route handler

   The fix is a separate export-only Next config/project, or a filtered copy of `app/` containing admin and auth only.
3. **Middleware coupling.** `src/middleware.ts` has a matcher (L1063) that already excludes `admin`, but `/auth/*` is matched. The blank-site allow-list (L662-675) also lists admin|auth|api|_next|media|health|ready. If Go serves `/auth`, its behaviour and the trailing-slash handling (`skipTrailingSlashRedirect`) must be mirrored.
4. **Dynamic `[id]` routes without `generateStaticParams`:**
   - `admin/entities/[id]`
   - `admin/entities/[id]/edit`
   - `admin/menus/[id]`
   - `admin/forms/[id]/edit`
   - `admin/forms/[id]/submissions`

   Export needs a placeholder param (via `generateStaticParams`) and a Go SPA fallback that serves that HTML for any id. Client code must read the real id from `usePathname()`. `useParams` returns the placeholder in static export, which is unverified.
5. **Favourable facts (not blockers).** Admin pages are `'use client'`. Exceptions not yet inspected: `admin/settings/page.tsx` and `admin/linking/equity/page.tsx`. `admin/layout.tsx` is a trivial server component (`<link /admin.css>` plus `AdminShell`). Auth is client-side (in-memory token plus HttpOnly cookie, `lib/api.ts` L15/L386), so there are no server session checks. `next/image` is unused in admin. `/admin.css` and `/public.css` are prebuilt into `frontend/public/` (`package.json` L16-21).
6. **Loss of `headers()`.** CSP (report-only), `X-Frame-Options` and `Cache-Control: no-store` for `/admin/*` currently come from `next.config.js`. Go must re-add them for admin.

## Recommended follow-up

1. Create a separate export-only Next config/project (or filtered `app/` copy) containing only `admin` and `auth`, with its own root layout.
2. Add a placeholder `generateStaticParams` to the six `[id]` routes and read the real id from `usePathname()`.
3. Embed the export in the Go binary. Serve it with an SPA fallback so any `/admin/**/<id>` returns the placeholder HTML.
4. Re-add CSP, `X-Frame-Options` and `no-store` for admin responses in Go.
5. Mirror `/auth` routing and trailing-slash behaviour in Go.
6. (Done in v1-15: `hatamoto-web` is gone from `scripts/ship.sh` and Caddy.)

## This PR

Next keeps `/admin` and `/auth` (and `/_next/*`, `/admin.css`). Caddy routes those to `localhost:3001`. See `docs/deploy/caddy-hatamoto.md`.
