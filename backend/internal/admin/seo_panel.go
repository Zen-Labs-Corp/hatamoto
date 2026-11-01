package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/net/html"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/seo"
)

// The shared SEO panel (blog editor, entity form): a port of the React seo-analysis-panel
// (length checks, focus keyword, headings, images, links, readability) plus the
// answerability checks that make a post easy for an assistant to quote.
// It is pure: the handler feeds it the draft as posted and renders the result,
// so the panel on first load, after a save and after each background refresh is
// the same code.

const (
	checkPass = "pass"
	checkWarn = "warn"
	checkFail = "fail"
)

type seoCheck struct{ ID, Name, Status, Message string }

// PanelMode says what the panel analyses.
type PanelMode int

const (
	// ModeBody runs every check, including the ones that read the body.
	ModeBody PanelMode = iota
	// ModeMetaOnly runs the search-result length checks and the keyword checks
	// on the title, description and address only. For content with no body
	// (taxonomy terms, entries without a text field).
	ModeMetaOnly
)

// seoInput is the draft as the panel sees it.
type seoInput struct {
	Title, MetaTitle, MetaDescription, Slug, Excerpt, Content string
	Keyword                                                   string
	Canonical, BaseURL, Path                                  string
	// OGTitle, OGDescription and OGImage are the hand-written social values;
	// Social asks for the share card (entries do, terms do not).
	OGTitle, OGDescription, OGImage string
	Social                          bool
	InLLMs, Published               bool
	Mode                            PanelMode
	// NoKeyword and NoLLMs leave out the focus-keyword and llms.txt checks for
	// a form that has no such field to act on.
	NoKeyword, NoLLMs bool

	// SEO, when set, resolves the preview title and description with the same
	// function the public head uses, so the preview matches the live page.
	// SchemaSlug picks the type's templates.
	SEO                           *seo.Settings
	SchemaSlug, SiteName, Tagline string

	// Kind picks the template the preview resolves with; the zero value is
	// KindSingle, an entry. A term sets KindTaxonomy with TermKind (the
	// taxonomy slug) and Term (its name). Home previews the Home template and
	// wins over Kind.
	Kind     seo.Kind
	TermKind string
	Term     string
	Home     bool
}

type serpPreview struct {
	Title, URL, Description string
	TitleLen, DescLen       int
	TitleStatus, DescStatus string
	TitleMsg, DescMsg       string
}

// socialCard is the share card as the public head will carry it: the social
// title, description and image, each resolved hand-written > template or meta
// description > default. Show is false for forms with no social fields.
type socialCard struct {
	Show                            bool
	Title, Description, Image, Site string
}

type seoPanel struct {
	SERP           serpPreview
	Card           socialCard
	SEO, Answer    []seoCheck
	Words          int
	ReadingMinutes int
	FAQ            int
	// Pass, Warn, Fail and Total count every check the panel shows (the two
	// search-result length checks, the search checks and the answer checks).
	// Score turns the counts into one number.
	Pass, Warn, Fail, Total int
	// JSONLD is the structured data the published page will carry (pretty
	// printed), JSONLDType its type(s); empty when it cannot be built.
	JSONLD, JSONLDType string
	// JSONLDNote replaces the "no name" message when no preview is built.
	JSONLDNote string
}

type heading struct {
	Level int
	Text  string
}

// docStats is what the checks read out of the body's HTML.
type docStats struct {
	Text       string
	Words      int
	Headings   []heading
	Paragraphs []string // top-level paragraphs in order
	// AfterHeading[i] is the first paragraph after Headings[i] ("" when none).
	AfterHeading []string
	Images       int
	NoAlt        int
	Internal     int
	External     int
	FAQ          int
}

// countWords counts the words of plain text.
func countWords(s string) int { return len(strings.Fields(s)) }

// readingMinutes is the reading time at 200 words a minute, at least one.
func readingMinutes(words int) int { return max(1, (words+100)/200) }

func attrOf(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.Data == "br" || n.Data == "p" || n.Data == "li") {
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && n.Data != "span" && n.Data != "a" && n.Data != "strong" && n.Data != "em" {
			b.WriteByte(' ')
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// analyzeBody reads the body HTML. siteHost is the host that makes an absolute
// link internal.
func analyzeBody(content, siteHost string) docStats {
	var st docStats
	root, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return st
	}
	st.Text = textOf(root)
	st.Words = countWords(st.Text)
	st.FAQ = len(entity.ExtractFAQItems(content))
	pendingHeading := -1
	inFAQ := false // a FAQ block's questions and answers are not outline headings or body paragraphs
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && strings.Contains(" "+attrOf(n, "class")+" ", " faq ") && !inFAQ {
			inFAQ = true
			defer func() { inFAQ = false }()
		}
		if n.Type == html.ElementNode && !inFAQ {
			switch n.Data {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				st.Headings = append(st.Headings, heading{Level: int(n.Data[1] - '0'), Text: textOf(n)})
				st.AfterHeading = append(st.AfterHeading, "")
				pendingHeading = len(st.Headings) - 1
			case "p":
				if t := textOf(n); t != "" {
					st.Paragraphs = append(st.Paragraphs, t)
					if pendingHeading >= 0 {
						st.AfterHeading[pendingHeading] = t
						pendingHeading = -1
					}
				}
			case "ul", "ol", "table", "blockquote":
				pendingHeading = -1
			}
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "img":
				st.Images++
				if strings.TrimSpace(attrOf(n, "alt")) == "" {
					st.NoAlt++
				}
			case "a":
				href := strings.TrimSpace(attrOf(n, "href"))
				switch {
				case href == "":
				case strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//"), strings.HasPrefix(href, "#"):
					st.Internal++
				default:
					if u, err := url.Parse(href); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
						if siteHost != "" && strings.EqualFold(u.Hostname(), siteHost) {
							st.Internal++
						} else {
							st.External++
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return st
}

// fleschReadingEase is the simplified Flesch score (0..100, higher is easier).
func fleschReadingEase(text string) int {
	words := countWords(text)
	sentences := 0
	for _, s := range strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '!' || r == '?' }) {
		if strings.TrimSpace(s) != "" {
			sentences++
		}
	}
	if sentences == 0 || words == 0 {
		return 0
	}
	score := 206.835 - 1.015*float64(words)/float64(sentences) - 84.6*float64(syllables(text))/float64(words)
	return int(max(0, min(100, score+0.5)))
}

func syllables(text string) int {
	total := 0
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		groups, inVowel := 0, false
		for _, r := range w {
			v := strings.ContainsRune("aeiouy", r)
			if v && !inVowel {
				groups++
			}
			inVowel = v
		}
		if strings.HasSuffix(w, "e") && groups > 1 {
			groups--
		}
		total += max(1, groups)
	}
	return total
}

func containsFold(text, needle string) bool {
	return needle != "" && strings.Contains(strings.ToLower(text), strings.ToLower(needle))
}

// questionHeading reports whether a heading is phrased as a question.
func questionHeading(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "?") {
		return true
	}
	first, _, _ := strings.Cut(strings.ToLower(s), " ")
	switch first {
	case "who", "what", "why", "how", "when", "where", "which", "can", "does", "do", "is", "are", "should", "will":
		return true
	}
	return false
}

func slugWords(s string) string {
	return strings.ToLower(strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), "-"))
}

func check(id, name, status, msg string) seoCheck {
	return seoCheck{ID: id, Name: name, Status: status, Message: msg}
}

// buildPanel runs every check on the draft.
func buildPanel(in seoInput) seoPanel {
	host := ""
	if u, err := url.Parse(in.BaseURL); err == nil {
		host = u.Hostname()
	}
	st := analyzeBody(in.Content, host)
	title := strings.TrimSpace(in.MetaTitle)
	if title == "" {
		title = strings.TrimSpace(in.Title)
	}
	desc := strings.TrimSpace(in.MetaDescription)
	if desc == "" {
		desc = strings.TrimSpace(in.Excerpt)
	}
	// The length and keyword checks measure what the author typed; the
	// preview shows what the page head will carry, site suffix included.
	checked := title
	if in.SEO != nil {
		r := seo.Resolve(in.SEO, in.resolverInput())
		title = strings.TrimSpace(r.Title)
		if r.Description != "" {
			desc = strings.TrimSpace(r.Description)
		}
	}
	meta := in.Mode == ModeMetaOnly
	p := seoPanel{Words: st.Words, ReadingMinutes: readingMinutes(st.Words), FAQ: st.FAQ}

	// Search-result preview and its length checks.
	addr := strings.TrimRight(in.BaseURL, "/") + in.Path
	if in.Canonical != "" {
		addr = in.Canonical
	}
	tl, dl := len([]rune(checked)), len([]rune(desc))
	p.SERP = serpPreview{Title: title, URL: addr, Description: desc, TitleLen: tl, DescLen: dl}
	switch {
	case tl == 0:
		p.SERP.TitleStatus, p.SERP.TitleMsg = checkFail, "Add a title."
	case tl < 30:
		p.SERP.TitleStatus, p.SERP.TitleMsg = checkFail, fmt.Sprintf("Title is too short (%d characters). Aim for 50 to 60.", tl)
	case tl < 50:
		p.SERP.TitleStatus, p.SERP.TitleMsg = checkWarn, fmt.Sprintf("Title could be longer (%d characters). Aim for 50 to 60.", tl)
	case tl > 60:
		p.SERP.TitleStatus, p.SERP.TitleMsg = checkWarn, fmt.Sprintf("Title may be cut off in results (%d characters). Keep it under 60.", tl)
	default:
		p.SERP.TitleStatus, p.SERP.TitleMsg = checkPass, fmt.Sprintf("Title is %d characters: a good length.", tl)
	}
	switch {
	case dl == 0:
		p.SERP.DescStatus, p.SERP.DescMsg = checkFail, "No description. Write one of 120 to 160 characters."
	case dl < 120:
		p.SERP.DescStatus, p.SERP.DescMsg = checkWarn, fmt.Sprintf("Description is short (%d characters). Aim for 120 to 160.", dl)
	case dl > 160:
		p.SERP.DescStatus, p.SERP.DescMsg = checkWarn, fmt.Sprintf("Description may be cut off (%d characters). Keep it under 160.", dl)
	default:
		p.SERP.DescStatus, p.SERP.DescMsg = checkPass, fmt.Sprintf("Description is %d characters: a good length.", dl)
	}

	if in.Social {
		p.Card = in.socialCard(checked, desc)
	}

	// Focus keyword.
	kw := strings.TrimSpace(in.Keyword)
	if kw == "" {
		if !in.NoKeyword {
			p.SEO = append(p.SEO, check("keyword", "Focus keyword", checkWarn, "No focus keyword set. Enter the phrase this entry should rank for."))
		}
	} else {
		first := ""
		if len(st.Paragraphs) > 0 {
			first = st.Paragraphs[0]
		}
		yes := func(ok bool, good, bad string) (string, string) {
			if ok {
				return checkPass, good
			}
			return checkFail, bad
		}
		s, m := yes(containsFold(checked, kw), "The keyword is in the title.", "The keyword is not in the title.")
		p.SEO = append(p.SEO, check("keyword-title", "Keyword in title", s, m))
		if !meta {
			s, m = yes(containsFold(first, kw), "The keyword is in the first paragraph.", "The keyword is not in the first paragraph.")
			p.SEO = append(p.SEO, check("keyword-intro", "Keyword in first paragraph", s, m))
		}
		s, m = yes(containsFold(desc, kw), "The keyword is in the description.", "The keyword is not in the description.")
		p.SEO = append(p.SEO, check("keyword-description", "Keyword in description", s, m))
		s, m = yes(strings.Contains(in.Slug, slugWords(kw)), "The keyword is in the address.", "The keyword is not in the address.")
		p.SEO = append(p.SEO, check("keyword-slug", "Keyword in address", s, m))
		if !meta {
			inHead := false
			for _, h := range st.Headings {
				inHead = inHead || containsFold(h.Text, kw)
			}
			s, m = yes(inHead, "The keyword is in a subheading.", "The keyword is not in any subheading.")
			p.SEO = append(p.SEO, check("keyword-heading", "Keyword in a subheading", s, m))
			n := strings.Count(strings.ToLower(st.Text), strings.ToLower(kw))
			switch {
			case n == 0:
				p.SEO = append(p.SEO, check("keyword-density", "Keyword use", checkWarn, "The keyword does not appear in the text."))
			case st.Words > 0 && float64(n*countWords(kw))/float64(st.Words) > 0.03:
				p.SEO = append(p.SEO, check("keyword-density", "Keyword use", checkWarn, fmt.Sprintf("The keyword appears %d times: that reads as stuffing. Use it naturally.", n)))
			default:
				p.SEO = append(p.SEO, check("keyword-density", "Keyword use", checkPass, fmt.Sprintf("The keyword appears %d times.", n)))
			}
		}
	}

	if !meta {
		// Structure and content.
		p.SEO = append(p.SEO, headingCheck(st))
		switch {
		case st.Images == 0:
			p.SEO = append(p.SEO, check("image-alt", "Image descriptions", checkWarn, "No images. A picture helps readers and search results."))
		case st.NoAlt > 0:
			p.SEO = append(p.SEO, check("image-alt", "Image descriptions", checkFail, fmt.Sprintf("%d of %d images have no description (alt text).", st.NoAlt, st.Images)))
		default:
			p.SEO = append(p.SEO, check("image-alt", "Image descriptions", checkPass, fmt.Sprintf("All %d images have a description.", st.Images)))
		}
		if st.Internal == 0 {
			p.SEO = append(p.SEO, check("links-internal", "Internal links", checkWarn, "No links to other pages of your site."))
		} else {
			p.SEO = append(p.SEO, check("links-internal", "Internal links", checkPass, fmt.Sprintf("%d links to your own pages.", st.Internal)))
		}
		if st.External == 0 {
			p.SEO = append(p.SEO, check("links-external", "External links", checkWarn, "No links to other sites. Cite a source where you can."))
		} else {
			p.SEO = append(p.SEO, check("links-external", "External links", checkPass, fmt.Sprintf("%d links to other sites.", st.External)))
		}
		switch {
		case st.Words < 300:
			p.SEO = append(p.SEO, check("length", "Length", checkFail, fmt.Sprintf("%d words. Entries under 300 words rarely rank.", st.Words)))
		case st.Words < 600:
			p.SEO = append(p.SEO, check("length", "Length", checkWarn, fmt.Sprintf("%d words. 600 or more covers a topic better.", st.Words)))
		default:
			p.SEO = append(p.SEO, check("length", "Length", checkPass, fmt.Sprintf("%d words (about %d minutes to read).", st.Words, p.ReadingMinutes)))
		}
		if st.Words >= 30 {
			switch score := fleschReadingEase(st.Text); {
			case score < 30:
				p.SEO = append(p.SEO, check("readability", "Readability", checkFail, fmt.Sprintf("Reading ease %d: hard going. Use shorter sentences and words.", score)))
			case score < 50:
				p.SEO = append(p.SEO, check("readability", "Readability", checkWarn, fmt.Sprintf("Reading ease %d: fairly difficult.", score)))
			default:
				p.SEO = append(p.SEO, check("readability", "Readability", checkPass, fmt.Sprintf("Reading ease %d: easy to read.", score)))
			}
		}

		p.Answer = answerChecks(st, in)
	}
	p.tally()
	return p
}

// tally counts the statuses of every check on the panel.
func (p *seoPanel) tally() {
	p.Pass, p.Warn, p.Fail = 0, 0, 0
	count := func(status string) {
		switch status {
		case checkPass:
			p.Pass++
		case checkWarn:
			p.Warn++
		case checkFail:
			p.Fail++
		}
	}
	count(p.SERP.TitleStatus)
	count(p.SERP.DescStatus)
	for _, c := range p.SEO {
		count(c.Status)
	}
	for _, c := range p.Answer {
		count(c.Status)
	}
	p.Total = p.Pass + p.Warn + p.Fail
}

// Score is a 0 to 100 summary of the checks: a pass counts 1, a warning 0.5 and
// a failure 0 of the Total, rounded to the nearest whole number. A panel with
// no checks scores 0. The counts stay the source of truth; the score only
// summarises them.
func (p seoPanel) Score() int {
	if p.Total == 0 {
		return 0
	}
	return (200*p.Pass + 100*p.Warn + p.Total) / (2 * p.Total)
}

// ScoreTone is the colour band of the score: "pass" from 80, "warn" from 50,
// else "fail"; "" when there are no checks.
func (p seoPanel) ScoreTone() string {
	switch s := p.Score(); {
	case p.Total == 0:
		return ""
	case s >= 80:
		return checkPass
	case s >= 50:
		return checkWarn
	}
	return checkFail
}

// ToFix is how many checks are not passing.
func (p seoPanel) ToFix() int { return p.Warn + p.Fail }

func headingCheck(st docStats) seoCheck {
	if len(st.Headings) == 0 {
		if st.Words > 300 {
			return check("headings", "Headings", checkWarn, "No subheadings. Break a long entry up with H2 headings.")
		}
		return check("headings", "Headings", checkPass, "A short entry needs no subheadings.")
	}
	prev := 1 // the title is the H1
	for _, h := range st.Headings {
		if h.Level == 1 {
			return check("headings", "Headings", checkWarn, "The body has an H1. The title is already the page's H1: use H2 and below.")
		}
		if h.Level > prev+1 {
			return check("headings", "Headings", checkWarn, fmt.Sprintf("A heading jumps from H%d to H%d. Do not skip levels.", prev, h.Level))
		}
		prev = h.Level
	}
	return check("headings", "Headings", checkPass, fmt.Sprintf("%d subheadings in order.", len(st.Headings)))
}

// answerChecks are the answerability (AEO) checks.
func answerChecks(st docStats, in seoInput) []seoCheck {
	var out []seoCheck
	var questions, direct int
	for i, h := range st.Headings {
		if h.Level < 2 || !questionHeading(h.Text) {
			continue
		}
		questions++
		if a := st.AfterHeading[i]; a != "" && countWords(a) <= 60 {
			direct++
		}
	}
	if questions == 0 {
		out = append(out,
			check("question-headings", "Question headings", checkWarn, "No subheading is phrased as a question. Assistants quote the answer under the question people ask."),
			check("direct-answers", "Short direct answers", checkWarn, "Add a question heading, then answer it in a sentence or two."))
	} else {
		out = append(out, check("question-headings", "Question headings", checkPass, fmt.Sprintf("%d subheadings are questions.", questions)))
		if direct == questions {
			out = append(out, check("direct-answers", "Short direct answers", checkPass, "Every question is answered in a short paragraph (60 words or fewer) right under it."))
		} else {
			out = append(out, check("direct-answers", "Short direct answers", checkWarn, fmt.Sprintf("%d of %d questions are not followed by a short paragraph (60 words or fewer).", questions-direct, questions)))
		}
	}
	if n := len(st.Paragraphs); n > 0 {
		switch w := countWords(st.Paragraphs[0]); {
		case w < 10:
			out = append(out, check("lead", "Opening summary", checkWarn, "The first paragraph is very short. Open with a one or two sentence summary."))
		case w > 60:
			out = append(out, check("lead", "Opening summary", checkWarn, fmt.Sprintf("The first paragraph is %d words. Open with a summary of 60 words or fewer.", w)))
		default:
			out = append(out, check("lead", "Opening summary", checkPass, "The entry opens with a short summary."))
		}
	}
	if st.FAQ > 0 {
		out = append(out, check("faq", "FAQ block", checkPass, fmt.Sprintf("%d questions in FAQ blocks are published as FAQPage structured data.", st.FAQ)))
	} else {
		out = append(out, check("faq", "FAQ block", checkWarn, "No FAQ block. Add one with / then FAQ to publish FAQPage structured data."))
	}
	switch {
	case in.NoLLMs:
	case !in.InLLMs:
		out = append(out, check("llms", "llms.txt", checkWarn, "This entry is left out of /llms.txt. Tick the box in the sidebar to list it."))
	case in.Published:
		out = append(out, check("llms", "llms.txt", checkPass, "This entry is listed in /llms.txt."))
	default:
		out = append(out, check("llms", "llms.txt", checkPass, "This entry will be listed in /llms.txt once it is published."))
	}
	return out
}

// resolverInput describes the page for the template resolver. Unless the input
// says otherwise the entry is a single page: the same kind the public head
// resolves it as.
func (in seoInput) resolverInput() seo.Input {
	kind := in.Kind
	switch {
	case in.Home:
		kind = seo.KindHome
	case kind == "":
		kind = seo.KindSingle
	}
	vars := seo.Vars{Title: strings.TrimSpace(in.Title), SiteName: in.SiteName, Tagline: in.Tagline, Excerpt: in.Excerpt, Term: in.Term}
	if kind == seo.KindTaxonomy {
		vars.Archive = in.Term
	}
	return seo.Input{
		Kind:            kind,
		TermKind:        in.TermKind,
		SchemaSlug:      in.SchemaSlug,
		HandTitle:       strings.TrimSpace(in.MetaTitle),
		HandDescription: strings.TrimSpace(in.MetaDescription),
		TitleSet:        strings.TrimSpace(in.MetaTitle) != "",
		DescriptionSet:  strings.TrimSpace(in.MetaDescription) != "",
		Vars:            vars,
	}
}

// socialCard resolves the share card the way the public head does: a
// hand-written value wins, then the type or taxonomy social title template (not
// site-suffixed), then the bare title; the image is the entry's, then the type's
// default, then the site default. A relative image is shown on the site address.
func (in seoInput) socialCard(title, desc string) socialCard {
	c := socialCard{Show: true, Title: strings.TrimSpace(in.OGTitle), Description: strings.TrimSpace(in.OGDescription), Image: strings.TrimSpace(in.OGImage), Site: in.SiteName}
	if in.SEO != nil && !in.SEO.Social.IsZero() {
		ri := in.resolverInput()
		if c.Title == "" {
			c.Title = strings.TrimSpace(seo.SocialTitle(in.SEO, ri))
		}
		if c.Image == "" {
			c.Image = seo.SocialImage(in.SEO, ri)
		}
		if c.Image == "" {
			c.Image = in.SEO.Social.Image
		}
	}
	if c.Title == "" {
		c.Title = title
	}
	if c.Description == "" {
		c.Description = desc
	}
	if strings.HasPrefix(c.Image, "/") && !strings.HasPrefix(c.Image, "//") {
		c.Image = strings.TrimRight(in.BaseURL, "/") + c.Image
	}
	return c
}

// withSEOSettings fills in what the preview needs to match the live head: the
// stored SEO settings, the type and the site name. A missing store or a read
// error leaves the preview on the entry's own values.
func (a *Admin) withSEOSettings(r *http.Request, in *seoInput, schemaSlug string) {
	if a.deps.SEO == nil {
		return
	}
	set, err := a.deps.SEO.Get(r.Context())
	if err != nil {
		return
	}
	in.SEO, in.SchemaSlug = &set, schemaSlug
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(r.Context()); err == nil && cfg != nil {
			in.SiteName, in.Tagline = cfg.CompanyName, cfg.Tagline
		}
	}
}

// markHome flags the preview as the homepage's when id is the entity the site
// serves at "/": the chosen one, or while none is chosen the page the public
// site falls back to. Without the stores it leaves the preview alone.
func (a *Admin) markHome(ctx context.Context, in *seoInput, id uuid.UUID) {
	if id == uuid.Nil {
		return
	}
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(ctx); err == nil && cfg != nil && cfg.HomepageEntityID != uuid.Nil {
			in.Home = cfg.HomepageEntityID == id
			return
		}
	}
	if a.deps.Growth.Explain == nil {
		return
	}
	if ex, err := a.deps.Growth.Explain.ExplainURL(ctx, "/"); err == nil {
		in.Home = ex.Page.EntityID == id.String()
	}
}
