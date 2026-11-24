package statichost

import "slices"

// Where says which origin answers an island in static mode.
type Where string

const (
	// Live is answered by the live origin for the static site.
	Live Where = "live"
	// Stays stays on Hatamoto and is never part of a static page.
	Stays Where = "stays"
)

// Island is one interactive part of a page.
type Island struct {
	Key    string   // stable id, e.g. "forms"
	Name   string   // English label for the Hosting page
	Routes []string // "METHOD /pattern" exactly as in server.RouteTable()
	Path   string   // only for a row without a route (search)
	Where  Where
	Note   string
}

var inventory = []Island{
	{Key: "forms", Name: "Contact form submit", Where: Live, Routes: []string{
		"POST /forms/{slug}/submit", "POST /api/v1/public/forms/{slug}/submit", "GET /api/v1/public/forms/{slug}/token"},
		Note: "file uploads ride the multipart submit"},
	{Key: "comments", Name: "Comment form", Where: Live, Routes: []string{
		"GET /-/comments/form/{entityID}", "POST /-/comments/{entityID}"},
		Note: "thread stays in the static HTML; form and token load live"},
	{Key: "search", Name: "Site search (results)", Where: Live, Path: "/search",
		Note: "served by the page catch-all, also /<locale>/search"},
	{Key: "consent", Name: "Consent", Where: Live, Routes: []string{
		"GET /api/v1/public/consent/region", "POST /api/v1/public/consent"}},
	{Key: "track", Name: "Pageview beacon", Where: Live, Routes: []string{"POST /api/v1/track"}},
	{Key: "plugins", Name: "Plugin public routes", Where: Live, Routes: []string{
		"GET /p/{plugin}/{rest...}", "POST /p/{plugin}/{rest...}", "PUT /p/{plugin}/{rest...}",
		"PATCH /p/{plugin}/{rest...}", "DELETE /p/{plugin}/{rest...}"}},

	// Stays on Hatamoto:
	{Key: "comment-links", Name: "Comment confirm and unsubscribe links", Where: Stays, Routes: []string{
		"GET /-/comments/confirm/{token}", "POST /-/comments/confirm/{token}",
		"GET /-/comments/unsubscribe/{token}", "POST /-/comments/unsubscribe/{token}"}},
	{Key: "comment-signin", Name: "Commenter sign-in and sign-out", Where: Stays, Routes: []string{
		"GET /-/comments/signin", "POST /-/comments/signin",
		"GET /-/comments/signin/{token}", "POST /-/comments/signin/{token}",
		"POST /-/comments/signout"}},
	{Key: "comment-own", Name: "Edit or delete one's own comment", Where: Stays, Routes: []string{
		"POST /-/comments/edit/{commentID}", "POST /-/comments/delete/{commentID}"}},
	{Key: "previews", Name: "Draft previews", Where: Stays, Routes: []string{"GET /preview/{token}"}},
	{Key: "form-page", Name: "Standalone form page", Where: Stays, Routes: []string{"GET /f/{slug}"}},
	{Key: "sign-in", Name: "Sign-in", Where: Stays, Routes: []string{
		"POST /api/v1/auth/login", "POST /api/v1/auth/logout"}},
	{Key: "agent-logs", Name: "Agent log ingest", Where: Stays, Routes: []string{"POST /api/v1/public/agent-logs"}},
	{Key: "mcp", Name: "MCP endpoint", Where: Stays, Routes: []string{"GET /mcp", "POST /mcp", "DELETE /mcp"}},
}

// Inventory returns a deep copy, so a caller cannot change the source of truth
// that the Hosting page and the route-table test both read.
func Inventory() []Island {
	out := make([]Island, len(inventory))
	for i, it := range inventory {
		it.Routes = slices.Clone(it.Routes)
		out[i] = it
	}
	return out
}
