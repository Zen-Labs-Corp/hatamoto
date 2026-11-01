package render

import (
	"strings"
	"time"

	"hatamoto.dev/core/internal/seo"
)

// socialHead fills the Open Graph and X tags of h. The core tags keep the order
// they always had; the optional ones from the Social settings follow. With no
// saved social settings and no hand-written social text the result is exactly
// the pre-social head.
func socialHead(h *headView, v *PageView, rawTitle string) {
	var so seo.Social
	if v.SEO != nil {
		so = v.SEO.Social
	}
	social := !so.IsZero()

	title := rawTitle
	if v.Page.OGTitle != "" {
		title = v.Page.OGTitle
	} else if social {
		if t := seo.SocialTitle(v.SEO, seoInput(v, rawTitle)); t != "" {
			title = t
		}
	}
	description := firstNonEmpty(v.Page.OGDescription, h.Description)

	image := v.Page.OGImage
	if image == "" && social {
		in := seoInput(v, rawTitle)
		image = absURL(v.BaseURL, firstNonEmpty(seo.SocialImage(v.SEO, in), so.Image))
	}

	article := social && so.Articles && isArticle(v)
	ogType := v.Page.OGType
	if ogType == "" {
		ogType = "website"
		if article {
			ogType = "article"
		}
	}

	h.OG = appendMeta(h.OG, "og:title", title)
	h.OG = appendMeta(h.OG, "og:description", description)
	h.OG = appendMeta(h.OG, "og:image", image)
	h.OG = appendMeta(h.OG, "og:type", ogType)
	h.OG = appendMeta(h.OG, "og:url", h.Canonical)

	card := "summary"
	if image != "" && so.Card != "summary" {
		card = "summary_large_image"
	}
	h.Twitter = appendMeta(h.Twitter, "twitter:card", card)
	h.Twitter = appendMeta(h.Twitter, "twitter:title", title)
	h.Twitter = appendMeta(h.Twitter, "twitter:description", description)
	h.Twitter = appendMeta(h.Twitter, "twitter:image", image)

	if !social {
		return
	}
	if so.SiteTags {
		h.OG = appendMeta(h.OG, "og:site_name", v.Site.Name)
		h.OG = appendMeta(h.OG, "og:locale", ogLocale(firstNonEmpty(v.Locale, v.Site.Language)))
	}
	if article {
		pub, mod := articleTimes(v)
		h.OG = appendMeta(h.OG, "article:published_time", formatTime(pub))
		h.OG = appendMeta(h.OG, "article:modified_time", formatTime(mod))
		h.OG = appendMeta(h.OG, "article:publisher", so.FBPage)
	}
	h.OG = appendMeta(h.OG, "fb:app_id", so.FBAppID)
	h.Twitter = appendMeta(h.Twitter, "twitter:site", so.TwitterSite)
}

// ogLocale turns a BCP 47 tag into the og:locale form: en-US becomes en_US.
func ogLocale(tag string) string {
	return strings.ReplaceAll(tag, "-", "_")
}

// isArticle reports whether v is the single view of a post. The post family
// mirrors entity.postFamilySlugs; render does not import entity.
func isArticle(v *PageView) bool {
	single := v.Kind == KindSingle || (v.Kind == "" && v.EntityID != "")
	if !single || v.EntityID == "" {
		return false
	}
	switch v.SchemaSlug {
	case "post", "blog", "article", "blog_post":
		return true
	}
	return false
}

// articleTimes returns the publish and update times of the entry, zero when
// unknown.
func articleTimes(v *PageView) (pub, mod time.Time) {
	pub, mod = v.PublishedAt, v.UpdatedAt
	if e := v.Entity; e != nil {
		if !e.PublishedAt.IsZero() {
			pub = e.PublishedAt
		}
		if !e.UpdatedAt.IsZero() {
			mod = e.UpdatedAt
		}
	}
	return pub, mod
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
