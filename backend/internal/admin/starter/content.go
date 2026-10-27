package starter

import "hatamoto.dev/core/internal/builder"

func section(id, typ string, order int, content map[string]any) builder.Section {
	return builder.Section{ID: id, Type: typ, SortOrder: order, Content: content}
}

// pageDoc is the page_doc of a starter page built from v1-shaped sections, so
// the starter renders exactly as before and never writes page_sections.
func pageDoc(secs ...builder.Section) *builder.Doc { return builder.FromSections(secs) }

func pages(o Options) []entry {
	return []entry{
		{
			slug: "home", name: o.SiteName, replaceSeed: true,
			attrs: map[string]any{
				"starter": true,
				"content": "<p>Welcome to " + escape(o.SiteName) + ".</p>",
				"page_doc": pageDoc(
					section("hero", "hero", 1, map[string]any{
						"headline":    o.SiteName,
						"subheadline": "<p>A fast, simple site you can change from the admin. Edit this page, add sections, write posts.</p>",
						"primaryCta":  "Read the blog", "ctaUrl": "/blog",
						"secondaryCta": "Get in touch", "secondaryCtaUrl": "/contact",
					}),
					section("features", "features", 2, map[string]any{
						"heading": "What you get", "variant": "cards", "columns": 3,
						"features": []any{
							map[string]any{"title": "Pages and posts", "description": "Write content once; the theme turns it into fast, accessible pages."},
							map[string]any{"title": "Yours to change", "description": "Every section on this page is editable from the admin, or replaced by your own."},
							map[string]any{"title": "Built to be found", "description": "Sitemaps, feeds and structured data are generated for you."},
						},
					}),
					section("cta", "cta", 3, map[string]any{
						"headline": "Ready to make it yours?", "subheadline": "<p>Edit this page, or start with the two example posts.</p>",
						"buttonText": "Read the first post", "buttonUrl": "/blog/hello-world", "backgroundStyle": "tinted",
					}),
				),
			},
		},
		{
			slug: "about", name: "About",
			attrs: map[string]any{
				"starter":     true,
				"description": "About " + o.SiteName,
				"content":     "<p>Tell visitors who you are and what " + escape(o.SiteName) + " is about.</p>",
				"page_doc": pageDoc(
					section("about", "content", 1, map[string]any{
						"layout": "stack", "headline": "About " + o.SiteName,
						"body": "<p>This is an example About page. Replace it with your story: who you are, what you do and why it matters.</p><p>Everything here is editable from the admin.</p>",
					}),
				),
			},
		},
		{
			slug: "contact", name: "Contact",
			attrs: map[string]any{
				"starter":     true,
				"description": "Contact " + o.SiteName,
				"content":     "<p>Send us a message.</p>",
				"page_doc": pageDoc(
					section("form", "contact_form", 1, map[string]any{
						"heading": "Get in touch", "subheading": "We read every message.", "form_slug": "contact", "variant": "centered",
					}),
					section("info", "contact_info", 2, map[string]any{"heading": "Other ways to reach us", "variant": "stacked"}),
				),
			},
		},
	}
}

func posts(o Options) []entry {
	return []entry{
		{
			slug: "hello-world", name: "Hello, world",
			attrs: map[string]any{
				"starter": true, "tags": []any{"welcome"},
				"excerpt": "Your first post: delete it, edit it or keep it.",
				"content": "<p>Welcome to " + escape(o.SiteName) + ". This is your first post. Edit or delete it from the admin, then write your own.</p>",
			},
		},
		{
			slug: "how-this-site-works", name: "How this site works",
			attrs: map[string]any{
				"starter": true, "tags": []any{"welcome", "guide"},
				"excerpt": "Pages, posts, menus and forms in two minutes.",
				"content": "<p>Pages live at their own address, posts live under <code>/blog</code>. Menus control the navigation, and the contact form on the Contact page sends messages to your inbox when email is set up.</p><h2>Next steps</h2><ul><li>Edit the home page</li><li>Write a post</li><li>Set your contact details in Settings</li></ul>",
			},
		},
	}
}

func escape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		case '&':
			out = append(out, "&amp;"...)
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
