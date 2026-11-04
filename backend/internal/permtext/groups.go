package permtext

import (
	"strings"

	"hatamoto.dev/sdk"
)

// Kind says how a statement is shown: allowed, needs the owner's review, or
// never possible.
type Kind string

const (
	KindOK     Kind = "ok"
	KindReview Kind = "review"
	KindNo     Kind = "no"
)

// Statement is one sentence of the install prompt: a bold title and an
// optional line of detail.
type Statement struct {
	Kind  Kind
	Title string
	Sub   string
	// Warn marks a statement that deserves a visible warning.
	Warn bool
}

// Grouped is the install prompt as two lists.
type Grouped struct {
	Can   []Statement
	Never []Statement
}

// ownNotes is the one sentence for the permissions that only touch the
// plugin's own storage.
var ownNotes = Statement{
	Kind:  KindOK,
	Title: "Keep its own notes",
	Sub:   "Run history and settings, stored apart from your site.",
}

var (
	prepareForReview = Statement{
		Kind:  KindReview,
		Title: "Prepare pages, posts, images, menus and redirects for you to review",
		Sub:   "Nothing goes live until you look at a preview and press Publish. You can undo it afterwards.",
	}
	suggestSiteWide = Statement{
		Kind:  KindReview,
		Title: "Suggest site-wide changes (site title, link structure, home page)",
		Sub:   "Each suggestion needs your own approval and a fresh sign-in.",
	}
)

// Group turns raw permission names into statements in input order. The
// permissions that only touch the plugin's own storage collapse into one
// "own notes" sentence at the position of the first of them. A plugin that
// declares runs gets the prepare-for-review sentence; one that proposes
// site-wide changes gets the suggestion sentence. A name this build does not
// know stays as its raw name, like Lookup.
func Group(perms []string, declaresRuns, proposes bool) Grouped {
	var g Grouped
	notes := false
	for _, p := range perms {
		switch p {
		case sdk.PermSettings, sdk.PermKV, sdk.PermDB:
			if !notes {
				g.Can = append(g.Can, ownNotes)
				notes = true
			}
			continue
		}
		t := Lookup(p)
		g.Can = append(g.Can, Statement{
			Kind:  KindOK,
			Title: strings.TrimPrefix(t.Help, "WARNING: "),
			Warn:  t.Warn,
		})
	}
	if declaresRuns {
		g.Can = append(g.Can, prepareForReview)
	}
	if proposes {
		g.Can = append(g.Can, suggestSiteWide)
	}
	g.Never = NeverList()
	return g
}

// NeverList is the fixed list of things no plugin can do, whatever it asks for.
func NeverList() []Statement {
	titles := []string{
		"Change your live site directly",
		"Edit or delete pages you wrote",
		"Touch user accounts, passwords, email settings or your site address",
		"Run code on your public pages",
	}
	out := make([]Statement, len(titles))
	for i, t := range titles {
		out[i] = Statement{Kind: KindNo, Title: t}
	}
	return out
}
