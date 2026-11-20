package identity

import (
	"html/template"
	"io"
)

// PageState selects what RenderPage shows.
type PageState int

const (
	// PageConfirm shows the Confirm button.
	PageConfirm PageState = iota
	// PageInvalid shows the expired or used link message.
	PageInvalid
)

// PageData is the input of RenderPage.
type PageData struct {
	SiteName    string
	EntityTitle string
	Action      string // "/comments/confirm/<token>", path only
	State       PageState
}

// pageTmpl is a standalone, unthemed page: no script and no external asset.
// The small inline style is allowed by the render CSP (style-src 'unsafe-inline').
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<meta name="referrer" content="no-referrer">
<title>{{if eq .State 0}}Confirm your comment{{else}}Link expired{{end}}</title>
<style>body{font:1rem/1.5 system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem}button{font:inherit;padding:.5rem 1.25rem}</style>
</head>
<body>
<main>
{{if eq .State 0}}<h1>Confirm your comment on {{.SiteName}}</h1>
<p>You are confirming your comment on "{{.EntityTitle}}".</p>
<form method="post" action="{{.Action}}"><button type="submit">Confirm</button></form>
{{else}}<h1>Link expired</h1>
<p>This link has expired or was already used. If you still want to comment, post it again and we will send a new link.</p>
{{end}}</main>
</body>
</html>
`))

// RenderPage writes the confirm or invalid-link page.
func RenderPage(w io.Writer, d PageData) error {
	return pageTmpl.Execute(w, d)
}
