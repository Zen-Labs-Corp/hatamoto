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
	// PageSignInForm asks for an email address (POST Action with hm_ct and email).
	PageSignInForm
	// PageSignInSent says a link was sent if the address can be used; one text for every outcome.
	PageSignInSent
	// PageSignedIn says this browser is signed in and offers sign-out (POST Action with hm_ct).
	PageSignedIn
	// PageSignedOut says this browser is signed out.
	PageSignedOut
	// PageSignInLink shows the Sign in button of a mailed link (POST Action).
	PageSignInLink
	// PageSignInInvalid shows the expired or used sign-in link message.
	PageSignInInvalid
)

// PageData is the input of RenderPage.
type PageData struct {
	SiteName    string
	EntityTitle string
	Action      string // path only: "/-/comments/confirm/<token>", "/-/comments/signin", ...
	State       PageState
	Token       string // single-use form token (hm_ct) of the sign-in and sign-out forms
	Error       string // PageSignInForm: why the address was refused; plain text
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
<title>{{if eq .State 0}}Confirm your comment{{else if eq .State 1}}Link expired{{else if eq .State 6}}Sign in{{else if eq .State 7}}Link expired{{else}}Sign in to comment{{end}}</title>
<style>body{font:1rem/1.5 system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem}button{font:inherit;padding:.5rem 1.25rem}</style>
</head>
<body>
<main>
{{if eq .State 0}}<h1>Confirm your comment on {{.SiteName}}</h1>
<p>You are confirming your comment on "{{.EntityTitle}}".</p>
<form method="post" action="{{.Action}}"><button type="submit">Confirm</button></form>
{{else if eq .State 1}}<h1>Link expired</h1>
<p>This link has expired or was already used. If you still want to comment, post it again and we will send a new link.</p>
{{else if eq .State 2}}<h1>Sign in to comment on {{.SiteName}}</h1>
{{if .Error}}<p role="alert">{{.Error}}</p>{{end}}
<p>Enter your email address and we will send you a sign-in link.</p>
<form method="post" action="{{.Action}}"><input type="hidden" name="hm_ct" value="{{.Token}}"><p><label>Email address <input type="email" name="email" autocomplete="email" required></label></p><button type="submit">Send sign-in link</button></form>
{{else if eq .State 3}}<h1>Check your email</h1>
<p>If this address can be used, we sent a sign-in link to it. The link works once and expires in 24 hours.</p>
{{else if eq .State 4}}<h1>You are signed in</h1>
<p>You are signed in on this browser.</p>
<form method="post" action="{{.Action}}"><input type="hidden" name="hm_ct" value="{{.Token}}"><button type="submit">Sign out</button></form>
{{else if eq .State 5}}<h1>You are signed out</h1>
<p>You are signed out on this browser.</p>
{{else if eq .State 6}}<h1>Sign in to {{.SiteName}}</h1>
<form method="post" action="{{.Action}}"><button type="submit">Sign in</button></form>
{{else}}<h1>Link expired</h1>
<p>This link has expired or was already used. Request a new sign-in link to comment.</p>
{{end}}</main>
</body>
</html>
`))

// RenderPage writes the confirm, invalid-link or sign-in page for d.State.
func RenderPage(w io.Writer, d PageData) error {
	return pageTmpl.Execute(w, d)
}
