---
title: What does not come across from WordPress
nav_title: Not imported from WordPress
description: What the WordPress importer leaves behind, such as comments, shops, page-builder layouts, forms, plugin data and themes, and what to do about each.
---

This page lists what the WordPress importer does not bring over, so you can plan for it before you move. What it does import, and how, is in the [WordPress import reference](wordpress-import-reference.md); to run an import, follow [Import a WordPress site](wordpress-import.md). The scan report names most of these for your site before anything is written.

## Overview

| WordPress has | What happens | What to do |
|---|---|---|
| Comments | Not imported. Hatamoto has no comments. | Export them from WordPress first if you want to keep them, or link to a hosted comment service in your theme. |
| Shop (WooCommerce) and events | Orders and coupons are skipped. Products and events come across as plain pages of a custom type (text and images only: no prices, cart, stock or event dates logic). Hatamoto does not do e-commerce. | Deselect those types in the scan, or delete them after the import, and keep the shop where it is. |
| Page-builder layouts (Elementor, Divi, WPBakery, Avada, Beaver Builder, Bricks) | The text is kept; the layout is not rebuilt. The page is marked low fidelity and listed for review. | Rebuild the page in the Hatamoto page editor, using the imported text. |
| Forms (Contact Form 7, Gravity Forms, WPForms, Ninja Forms, Formidable) | The form markup and shortcode are removed and reported. | Recreate the form with Hatamoto forms. |
| Other shortcodes | Removed from the text and listed in the report. | Replace each with a block, a plugin or plain content. |
| User accounts and passwords | Not imported. Authors become people (author pages), not sign-in accounts, and are not public until you switch Public profile on. | Invite your editors from Settings > Users. |
| Theme and customizer settings | Not imported. Custom CSS is skipped. | Pick or build a [theme](../themes/index.md). |
| Plugins and their data | Not imported. They are listed as unsupported. | Look for a Hatamoto [plugin](../plugins/index.md), or do without. |
| Widgets and sidebars | Not imported. | Add the same content to your layout parts or sections. |
| Revisions | Not imported; the importer skips revision posts. | Keep a WordPress export as your archive. |
| Public pages in other languages of a multilingual site (WPML, Polylang) | Every language is imported and tagged with its locale, but only the site language is public and the others get no redirects. | Keep them stored until a multilingual build exists, or run a separate Hatamoto site per language. |
| Private and password-protected content | Imported as drafts, never published. | Review each draft and decide. |
| SEO plugin data beyond title, description, canonical, noindex and social image | Not imported. | Check the pages that matter in the SEO panel. |
| Plain-permalink URLs (`?p=12`) | The content imports, but old URLs are audited from the sitemap only. | Check the URL audit report and add redirects for any that are missing. |

## Before you cut over

1. Run a scan (`--scan-only`, or the wizard's scan page) and read the plugins, page builders and shortcodes it lists. Those are the pages that will need hands-on work.
2. Open every page the report marks low fidelity and compare it with the old site.
3. Run the URL audit and fix every URL it flags. Old addresses are the part of a move that costs you search traffic if they break.

## Related

- [WordPress import reference](wordpress-import-reference.md)
- [Redirect rules](redirect-rules.md)
