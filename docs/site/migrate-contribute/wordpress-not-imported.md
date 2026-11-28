---
title: What does not come across from WordPress
nav_title: Not imported from WordPress
description: What the WordPress importer leaves behind, such as shops, page-builder layouts, some forms, plugin data and themes, and what to do about each.
---

This page lists what the WordPress importer does not bring over, so you can plan for it before you move. What it does import, and how, is in the [WordPress import reference](wordpress-import-reference.md); to run an import, follow [Import a WordPress site](wordpress-import.md). The scan report names most of these for your site before anything is written.

## Overview

| WordPress has | What happens | What to do |
|---|---|---|
| Comments | Imported from a WXR export file, with their status, threading and dates (see [the import reference](wordpress-import-reference.md)). A live (REST) import brings none. Pingbacks, trackbacks, empty comments, comments over 16 KiB and comments without a usable email are skipped. Importing never turns comments on. | Import a WXR file to keep them, then turn comments on in the site settings when you want them. |
| Shop (WooCommerce) and events | Orders and coupons are skipped. Products and events come across as plain pages of a custom type (text and images only: no prices, cart, stock or event dates logic). Hatamoto does not do e-commerce. | Deselect those types in the scan, or delete them after the import, and keep the shop where it is. |
| Page-builder layouts (Elementor, Divi, WPBakery, Avada, Beaver Builder, Bricks) | WPBakery content is converted to plain HTML (headings, images, buttons, raw HTML, map links) and only the layout is lost, a small loss in the report. For the other builders the text is kept, the layout is not rebuilt, and the page is listed for review. | Rebuild the layout in the Hatamoto page editor, using the imported content. |
| Forms: Contact Form 7 (WXR file only) | The forms become Hatamoto forms with the WordPress slug, and `[contact-form-7 ...]` in a body is replaced by `[form slug=...]`. Mail settings and notifications are not imported. Quiz, captcha and conditional add-on rules are dropped and listed in the report. A live (REST) import brings no forms. | Set the recipient under Forms, then send a test entry. |
| Forms: Gravity Forms, WPForms, Ninja Forms, Formidable | The form markup and shortcode are removed and reported. | Recreate the form with Hatamoto forms. |
| Other shortcodes | Removed from the text and listed in the report. | Replace each with a block, a plugin or plain content. |
| User accounts and passwords | Not imported. Authors become people (author pages), not sign-in accounts, and are not public until you switch Public profile on. | Invite your editors from Settings > Users. |
| Theme and customizer settings | Not imported, except Additional CSS: the live custom CSS comes across into Theme > Additional CSS. Parts that are refused, such as `@import`, are kept there as a comment and listed in the report. | Pick or build a [theme](../themes/index.md). Add fonts under Theme > Colours and fonts > Fonts. |
| Custom fields (ACF) | Imported from a WXR file with field group definitions: see [Custom fields (ACF)](wordpress-import-reference.md#custom-fields-acf). ACF options pages are not imported. A live (REST) import brings no ACF values. On posts and pages the values are kept as data, without editor fields. | Check the report for lines marked Partly or Skipped. |
| Plugins and their data | Not imported, except ACF custom fields and Contact Form 7 forms (see the rows above). They are listed as unsupported. | Look for a Hatamoto [plugin](../plugins/index.md), or do without. |
| Widgets and sidebars | Not imported. | Add the same content to your layout parts or sections. |
| Revisions | Not imported; the importer skips revision posts. | Keep a WordPress export as your archive. |
| Public pages in other languages of a multilingual site (WPML, Polylang) | Every language is imported and tagged with its locale. An item is public only if its locale is one of the site's public locales (Settings > Locales); otherwise it is stored but not public. Items in other languages get no redirects, and links to them are not rewritten. | Add the languages you want public in Settings > Locales before or after the import. |
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
