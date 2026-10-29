package packages

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/permtext"
)

//go:embed page.html.tmpl
var pageSource string

var pageTemplate = template.Must(template.New("page").Parse(pageSource))

// pageCSP is the page's Content-Security-Policy (a meta tag, because the page is a
// static file; frame-ancestors is ignored there and is sent by the web server). The
// one inline style and the one inline script are allowed by their sha256 hash, so
// there is no 'unsafe-inline' and no nonce. Neither block contains a template action,
// so the hashed bytes are exactly the bytes in the rendered page; the test recomputes
// the hashes from the rendered output to prove it.
var pageCSP = "default-src 'none'; style-src " + inlineHash(pageSource, "style") +
	"; script-src " + inlineHash(pageSource, "script") + "; base-uri 'none'; form-action 'none'"

// inlineHash returns the CSP source expression for the text of the single inline
// <tag> element of src. It panics when there is not exactly one such element: a page
// edit that adds a second block must not silently ship an unhashed one.
func inlineHash(src, tag string) string {
	open, closing := "<"+tag+">", "</"+tag+">"
	if strings.Count(src, open) != 1 || strings.Count(src, closing) != 1 {
		panic("packages: page template must have exactly one inline <" + tag + "> element")
	}
	_, rest, _ := strings.Cut(src, open)
	body, _, _ := strings.Cut(rest, closing)
	sum := sha256.Sum256([]byte(body))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// PageOptions are the words and links of a rendered directory page. Empty
// URLs hide their button; an empty InstallHint hides the hint line.
type PageOptions struct {
	Title, Intro, WriteURL, SubmitURL, InstallHint string
}

type pageCard struct {
	Heading, Name, Version, Description, Author, TierLabel, Source, Search string
	PermCount                                                              int
	PermWarn                                                               []string
	PermLines                                                              []permtext.Line
}

type pageData struct {
	Title, Intro, WriteURL, SubmitURL, InstallHint string
	CSP                                            string
	Cards                                          []pageCard
}

// SafeHTTPS returns s when it is an https URL with a host and no credentials,
// and "" otherwise, so a hostile value never becomes a link.
func SafeHTTPS(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

// RenderPage renders the plugin entries of idx (sorted as in the index) as one
// self-contained HTML page: inline style and script, no remote assets. The
// index is expected to come from ParseIndex.
func RenderPage(idx *Index, opts PageOptions) ([]byte, error) {
	if opts.Title == "" {
		opts.Title = "Plugins"
	}
	if opts.Intro == "" {
		opts.Intro = "Plugins available from this registry."
	}
	data := pageData{
		Title: opts.Title, Intro: opts.Intro, InstallHint: opts.InstallHint, CSP: pageCSP,
		WriteURL: SafeHTTPS(opts.WriteURL), SubmitURL: SafeHTTPS(opts.SubmitURL),
	}
	for _, e := range idx.Packages {
		if e.Kind != KindPlugin {
			continue
		}
		c := pageCard{
			Heading: cmp.Or(e.Title, e.Name), Name: e.Name, Version: displayVersion(e.Version), Description: e.Description,
			Source: SafeHTTPS(e.Source), PermCount: len(e.Permissions),
			Search: strings.ToLower(e.Title + " " + e.Name + " " + e.Description),
		}
		pc := permtext.ForCard(e.Permissions)
		c.PermWarn, c.PermLines = pc.Warn, pc.Lines
		switch e.Tier {
		case TierFirstParty:
			c.TierLabel = "First-party"
		case TierCommunity:
			c.TierLabel = "Community"
			c.Author = e.Author
		}
		data.Cards = append(data.Cards, c)
	}
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render page: %w", err)
	}
	return buf.Bytes(), nil
}

// displayVersion shows a version as "v1.2.0" whether or not the index has the "v".
func displayVersion(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
