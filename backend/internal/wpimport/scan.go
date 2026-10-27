package wpimport

import (
	"fmt"
	"sort"
	"strings"
)

// ItemNote is an item the report points at, with the reason.
type ItemNote struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Link   string `json:"link"`
	Reason string `json:"reason"`
}

// Fidelity counts items by how faithfully they import.
type Fidelity struct {
	High    int `json:"high"`
	Medium  int `json:"medium"`
	Low     int `json:"low"`
	Percent int `json:"percent"` // share of items imported without loss
}

// Report is the scan of a source: what is there and what will not survive.
type Report struct {
	Source string `json:"source"`
	Site   Site   `json:"site"`

	Posts       int            `json:"posts"`
	Pages       int            `json:"pages"`
	CustomTypes map[string]int `json:"custom_types,omitempty"`
	Drafts      int            `json:"drafts"`
	Private     int            `json:"private"`
	Media       int            `json:"media"`
	Terms       int            `json:"terms"`
	Authors     int            `json:"authors"`
	Menus       int            `json:"menus"`
	OldURLs     int            `json:"old_urls"`

	// SiteMode is "empty" or "existing" (see InspectSite); set by ApplySiteState.
	SiteMode        SiteMode `json:"site_mode,omitempty"`
	ExistingContent int      `json:"existing_content,omitempty"`
	ModeNote        string   `json:"mode_note,omitempty"`

	Permalink        string `json:"permalink"`
	PermalinkPattern string `json:"permalink_pattern,omitempty"`
	// PermalinkKept is true when the new URL pattern reproduces the old URLs;
	// otherwise every changed URL gets a redirect.
	PermalinkKept bool `json:"permalink_kept"`

	SEOPlugin          string         `json:"seo_plugin,omitempty"`
	PageBuilders       []string       `json:"page_builders,omitempty"`
	Forms              []string       `json:"forms,omitempty"`
	Multilingual       []string       `json:"multilingual,omitempty"`
	DefaultLang        string         `json:"default_language,omitempty"`
	Languages          map[string]int `json:"languages,omitempty"`
	Shortcodes         map[string]int `json:"shortcodes,omitempty"`
	UnsupportedPlugins []string       `json:"unsupported_plugins,omitempty"`

	LowFidelity []ItemNote `json:"low_fidelity,omitempty"`
	MembersOnly []ItemNote `json:"members_only,omitempty"`
	Embeds      int        `json:"embeds"`
	Fidelity    Fidelity   `json:"fidelity"`
	Warnings    []string   `json:"warnings,omitempty"`
}

// mapped namespaces are handled (or reported) by name; they are not "unsupported".
var mappedNamespaces = map[string]string{
	"yoast/": "Yoast SEO", "rankmath/": "Rank Math", "seopress/": "SEOPress", "aioseo/": "All in One SEO",
	"contact-form-7/": "Contact Form 7", "gravityforms/": "Gravity Forms", "wpforms/": "WPForms", "ninja-forms": "Ninja Forms",
	"elementor/": "Elementor", "pll/": "Polylang", "wpml/": "WPML",
}

var formPlugins = map[string]bool{"Contact Form 7": true, "Gravity Forms": true, "WPForms": true, "Ninja Forms": true}

// Scan reads the Dump and reports what the import will do, before anything is written.
func Scan(d *Dump, source string) *Report {
	r := &Report{Source: source, Site: d.Site, Media: len(d.Media), Terms: len(d.Terms), Authors: len(d.Authors),
		Menus: len(d.Menus), OldURLs: len(d.OldURLs), Permalink: d.Site.Permalink, SEOPlugin: d.Detect.SEOPlugin,
		Multilingual: append([]string(nil), d.Detect.Multilingual...), DefaultLang: d.Detect.DefaultLang,
		Languages:    languageCounts(d.Items, d.Site.Language),
		PageBuilders: append([]string(nil), d.Detect.PageBuilders...), Shortcodes: map[string]int{},
		Warnings: append([]string(nil), d.Warnings...), CustomTypes: map[string]int{}}
	if p, ok := PatternFromPermalink(d.Site.Permalink); ok {
		r.PermalinkPattern, r.PermalinkKept = p, true
	}
	var forms []string
	for _, ns := range d.Detect.Namespaces {
		name, mapped := "", false
		for prefix, n := range mappedNamespaces {
			if strings.HasPrefix(ns, prefix) {
				name, mapped = n, true
			}
		}
		switch {
		case mapped && formPlugins[name]:
			forms = appendUnique(forms, name)
		case mapped && name == "Elementor":
			r.PageBuilders = appendUnique(r.PageBuilders, name)
		case mapped:
		case coreNamespaces[ns]:
		default:
			r.UnsupportedPlugins = appendUnique(r.UnsupportedPlugins, ns)
		}
	}
	for _, it := range d.Items {
		if it.Status == wpTrash || it.Status == "auto-draft" {
			continue
		}
		switch it.Type {
		case "post":
			r.Posts++
		case "page":
			r.Pages++
		default:
			r.CustomTypes[it.Type]++
		}
		switch it.Status {
		case wpPrivate:
			r.Private++
			r.MembersOnly = append(r.MembersOnly, note(it, "private: imported as a draft"))
		case wpDraft, wpPending:
			r.Drafts++
		}
		if it.Protected {
			r.MembersOnly = append(r.MembersOnly, note(it, "password protected: imported as a draft"))
		}
		c := Convert(it.Content, d.RawBodies)
		r.Embeds += c.Embeds
		for _, sc := range c.Shortcodes {
			r.Shortcodes[sc]++
		}
		for _, f := range c.Forms {
			forms = appendUnique(forms, f)
		}
		reasons := []string{}
		level := 0 // 0 high, 1 medium, 2 low
		if len(c.Builders) > 0 {
			level = 2
			for _, b := range c.Builders {
				r.PageBuilders = appendUnique(r.PageBuilders, b)
			}
			reasons = append(reasons, "page builder ("+strings.Join(c.Builders, ", ")+"): layout is not kept, review the text")
		}
		if len(c.Shortcodes) > 0 {
			level = max(level, 1)
			reasons = append(reasons, "shortcodes removed: "+strings.Join(c.Shortcodes, ", "))
		}
		if len(c.Forms) > 0 {
			level = max(level, 1)
			reasons = append(reasons, "form removed: "+strings.Join(c.Forms, ", ")+" (recreate it with Hatamoto forms)")
		}
		if len(c.Dropped) > 0 {
			level = max(level, 1)
			reasons = append(reasons, "embeds removed: "+strings.Join(c.Dropped, ", "))
		}
		if it.Status == wpPrivate || it.Protected {
			level = max(level, 1)
		}
		switch level {
		case 0:
			r.Fidelity.High++
		case 1:
			r.Fidelity.Medium++
		default:
			r.Fidelity.Low++
		}
		if level > 0 && len(reasons) > 0 {
			r.LowFidelity = append(r.LowFidelity, note(it, strings.Join(reasons, "; ")))
		}
	}
	r.Forms = forms
	sort.Strings(r.Forms)
	sort.Strings(r.PageBuilders)
	sort.Strings(r.UnsupportedPlugins)
	if len(r.Languages) > 0 {
		var parts []string
		for _, k := range sortedKeys(r.Languages) {
			parts = append(parts, fmt.Sprintf("%s: %d items", k, r.Languages[k]))
		}
		r.Warnings = append(r.Warnings, fmt.Sprintf("languages found (%s): every language is imported and tagged, only the site language is public", strings.Join(parts, ", ")))
	} else if len(r.Multilingual) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("multilingual plugin (%s): no item language was found, so every item is imported in the site language", strings.Join(r.Multilingual, ", ")))
	}
	if !r.PermalinkKept && d.Site.Permalink != "" {
		r.Warnings = append(r.Warnings, fmt.Sprintf("permalink structure %s cannot be a Hatamoto URL pattern: every post URL changes and gets a 301 redirect", d.Site.Permalink))
	}
	total := r.Fidelity.High + r.Fidelity.Medium + r.Fidelity.Low
	if total > 0 {
		r.Fidelity.Percent = r.Fidelity.High * 100 / total
	}
	return r
}

func note(it Item, reason string) ItemNote {
	return ItemNote{ID: it.ID, Type: it.Type, Title: it.Title, Link: it.Link, Reason: reason}
}

// Summary is the report as text for the CLI.
func (r *Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source: %s (%s)\n", r.Site.URL, r.Site.Title)
	fmt.Fprintf(&b, "Content: %d posts, %d pages", r.Posts, r.Pages)
	for _, k := range sortedKeys(r.CustomTypes) {
		fmt.Fprintf(&b, ", %d %s", r.CustomTypes[k], k)
	}
	fmt.Fprintf(&b, "; %d drafts, %d private\n", r.Drafts, r.Private)
	fmt.Fprintf(&b, "Also: %d media files, %d terms, %d authors, %d menus, %d URLs in the old sitemap\n", r.Media, r.Terms, r.Authors, r.Menus, r.OldURLs)
	if r.ModeNote != "" {
		fmt.Fprintf(&b, "Target: %s\n", r.ModeNote)
	}
	if r.Permalink != "" {
		kept := "kept as " + r.PermalinkPattern
		if !r.PermalinkKept {
			kept = "not expressible: URLs change and redirect"
		}
		fmt.Fprintf(&b, "Permalinks: %s (%s)\n", r.Permalink, kept)
	}
	list := func(label string, v []string) {
		if len(v) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", label, strings.Join(v, ", "))
		}
	}
	list("SEO plugin", nonEmpty(r.SEOPlugin))
	list("Page builders", r.PageBuilders)
	list("Forms", r.Forms)
	list("Multilingual", r.Multilingual)
	list("Unsupported plugins", r.UnsupportedPlugins)
	if len(r.Shortcodes) > 0 {
		var parts []string
		for _, k := range sortedKeys(r.Shortcodes) {
			parts = append(parts, fmt.Sprintf("[%s] x%d", k, r.Shortcodes[k]))
		}
		list("Shortcodes (removed)", parts)
	}
	fmt.Fprintf(&b, "Embeds kept: %d\n", r.Embeds)
	fmt.Fprintf(&b, "Expected fidelity: %d%% of items import without loss (%d high, %d medium, %d low)\n", r.Fidelity.Percent, r.Fidelity.High, r.Fidelity.Medium, r.Fidelity.Low)
	for _, n := range r.LowFidelity {
		fmt.Fprintf(&b, "  - %s %q: %s\n", n.Type, n.Title, n.Reason)
	}
	for _, n := range r.MembersOnly {
		fmt.Fprintf(&b, "  - %s %q: %s\n", n.Type, n.Title, n.Reason)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	return b.String()
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
