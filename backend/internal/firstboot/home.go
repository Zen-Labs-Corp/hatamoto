// Package firstboot holds the home page a brand-new install seeds and the rule
// that recognises it (the wizard, the kit and the WordPress import treat it as
// replaceable).
package firstboot

import (
	"hatamoto.dev/core/internal/builder"
)

const (
	// SeedID is the hero; legacy seeds have only this section.
	SeedID = "welcome"
	// SeedIDPrefix starts the IDs of the other seeded sections.
	SeedIDPrefix = "welcome-"

	docs = "https://hatamoto.dev/docs/"
)

func section(id, typ string, order int, content map[string]any) builder.Section {
	return builder.Section{ID: id, Type: typ, SortOrder: order, Content: content}
}

func step(n, title, desc, where, path string) map[string]any {
	return map[string]any{
		"number": n, "title": title, "description": "<p>" + desc + "</p>",
		"where": where, "whereUrl": docs + path,
	}
}

// HomeDoc is the first-boot home page for siteName.
func HomeDoc(siteName string) *builder.Doc {
	return builder.FromSections([]builder.Section{
		section(SeedID, "hero", 1, map[string]any{
			"eyebrow":         "Hello world",
			"headline":        siteName,
			"subheadline":     "<p>This site is up and running. Sign in to replace this page with your own words, add pages and start writing.</p>",
			"primaryCta":      "Read the guide",
			"ctaUrl":          docs + "getting-started/first-run",
			"secondaryCta":    "Take the tour",
			"secondaryCtaUrl": docs + "admin/tour",
			"backdrop":        "grid",
		}),
		section(SeedIDPrefix+"make", "process_steps", 2, map[string]any{
			"eyebrow":    "Getting started",
			"heading":    "Make it yours",
			"subheading": "Each block on this page is a section. Edit, reorder or delete any of them from the page editor.",
			"layout":     "cards",
			"steps": []any{
				step("01", "Name and logo", "Set the site name, upload a logo and pick a brand colour.", "Settings › Site", "admin/settings-users-email"),
				step("02", "Edit this page", "Swap these sections for your own hero, services and contact details.", "Pages › Home", "admin/page-editor"),
				step("03", "Write a post", "Posts show up on the blog, in the RSS feed and in the sitemap.", "Posts › New", "admin/blog-and-people"),
				step("04", "Pick a theme", "Keep this one or install another from the theme directory.", "Theme › Themes", "themes/overview"),
			},
		}),
		section(SeedIDPrefix+"run", "process_steps", 3, map[string]any{
			"heading": "Run it properly",
			"layout":  "cards",
			"steps": []any{
				step("05", "Add a contact form", "Build a form, drop it on a page and get submissions by email, with spam filtering built in.", "Forms › New form", "admin/forms"),
				step("06", "Speed it up", "Turn on the page cache and preloading so visitors get pages served straight from memory.", "Settings › Performance", "admin/performance-cache"),
				step("07", "Turn on backups", "Choose how many backups to keep and copy them off-site so one bad day cannot take the site with it.", "Settings › Backups and recovery", "operate/backups"),
				step("08", "Lock it down", "Set up email delivery, require two-factor sign-in and move the admin to a private address.", "Settings › Access and security", "admin/accessibility-and-security"),
			},
		}),
		section(SeedIDPrefix+"import", "cta", 4, map[string]any{
			"headline":        "Moving from WordPress?",
			"subheadline":     "<p>Import posts, pages, media and redirects in one go.</p>",
			"buttonText":      "Import a site",
			"buttonUrl":       docs + "migrate-contribute/move-your-site",
			"backgroundStyle": "kasumi",
			"layout":          "split",
		}),
	})
}

// seedIDs is every section ID HomeDoc emits (SeedID alone is the old seed).
var seedIDs = map[string]bool{
	SeedID:                  true,
	SeedIDPrefix + "make":   true,
	SeedIDPrefix + "run":    true,
	SeedIDPrefix + "import": true,
}

// IsSeedIDs reports whether a page's section IDs are the first-boot seed's:
// at least one, each exactly one of the IDs HomeDoc emits. The old
// one-section seed matches too.
func IsSeedIDs(ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if !seedIDs[id] {
			return false
		}
	}
	return true
}
