package render

import (
	"net/url"
	"regexp"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

// videoRef is a video the facade knows how to embed.
type videoRef struct {
	Watch string // canonical page URL, the link target
	Embed string // iframe source, set only when entity.IframeSrcAllowed accepts it
}

var (
	ytID    = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	vimeoID = regexp.MustCompile(`^[0-9]{1,12}$`)
)

// parseVideo reads a YouTube (watch, short, shorts, embed) or Vimeo URL. The
// URLs it returns are built from the matched id, never copied from the input.
func parseVideo(raw string) (videoRef, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return videoRef{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	var ref videoRef
	switch host {
	case "youtu.be":
		if len(parts) == 1 && ytID.MatchString(parts[0]) {
			ref = youtubeRef(parts[0])
		}
	case "youtube.com", "m.youtube.com":
		switch {
		case len(parts) == 1 && parts[0] == "watch" && ytID.MatchString(u.Query().Get("v")):
			ref = youtubeRef(u.Query().Get("v"))
		case len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "embed") && ytID.MatchString(parts[1]):
			ref = youtubeRef(parts[1])
		}
	case "vimeo.com":
		if len(parts) == 1 && vimeoID.MatchString(parts[0]) {
			ref = videoRef{Watch: "https://vimeo.com/" + parts[0], Embed: "https://player.vimeo.com/video/" + parts[0]}
		}
	}
	if ref.Watch == "" {
		return videoRef{}, false
	}
	if !entity.IframeSrcAllowed(ref.Embed) {
		ref.Embed = "" // the link still works
	}
	return ref, true
}

func youtubeRef(id string) videoRef {
	return videoRef{Watch: "https://www.youtube.com/watch?v=" + id, Embed: "https://www.youtube-nocookie.com/embed/" + id}
}

// localImage returns u when it is a path on this site: widgets that must not
// cause a third-party request at load only take media of the site itself.
func localImage(u string) string {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") || strings.HasPrefix(u, `/\`) {
		return ""
	}
	return imgSrc(u)
}

func init() {
	registerWidget("video", func(w *widgetCtx) (any, error) {
		w.Class = "hm-vid"
		x := struct{ Href, Embed, Title, Poster string }{Title: str(w.P, "title"), Poster: localImage(str(w.P, "poster"))}
		if x.Title == "" {
			x.Title = "Video"
		}
		raw := str(w.P, "url")
		if ref, ok := parseVideo(raw); ok {
			x.Href, x.Embed = ref.Watch, ref.Embed
		} else if raw != "" {
			x.Href = safeURL(raw) // not a known provider: a plain link, never an iframe
		}
		return x, nil
	})
	registerWidget("map", func(w *widgetCtx) (any, error) {
		q := str(w.P, "query")
		x := struct{ Href, Image, Alt string }{Image: localImage(str(w.P, "image")), Alt: str(w.P, "alt")}
		if x.Alt == "" {
			x.Alt = q
		}
		if h := str(w.P, "href"); h != "" {
			x.Href = safeURL(h)
		} else {
			x.Href = "https://www.openstreetmap.org/search?query=" + url.QueryEscape(q)
		}
		return x, nil
	})
	registerWidget("tabs", func(w *widgetCtx) (any, error) {
		w.Class = "hm-tabs"
		return nil, nil
	})
	registerWidget("tabpanel", func(w *widgetCtx) (any, error) {
		w.Class = "hm-tp"
		return struct{ Label string }{str(w.P, "label")}, nil
	})
}
