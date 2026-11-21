package admin

import "context"

// staticNote is the warning on the Crawlers page for a site served as static
// files: crawler hits on the CDN never reach Hatamoto.
type staticNote struct{ Body, Href, LinkText string }

// staticNoteFor is the warning when the mode is static and no access-log
// ingest is configured, nil otherwise.
func staticNoteFor(mode string, ingest bool) *staticNote {
	if mode != HostingStatic || ingest {
		return nil
	}
	return &staticNote{
		Body:     "This site is served as static files. AI crawler hits on your CDN are not counted here. Send your CDN or server access logs to Hatamoto (the Caddy log tailer or POST /agent-logs) to keep them.",
		Href:     ingestDocsURL,
		LinkText: "How to set up",
	}
}

// staticNote reads the saved mode; no service or a failed read shows nothing.
func (a *Admin) staticNote(ctx context.Context) *staticNote {
	if a.deps.Hosting == nil {
		return nil
	}
	v, err := a.deps.Hosting.View(ctx)
	if err != nil {
		return nil
	}
	return staticNoteFor(v.Mode, a.deps.AgentLogIngest)
}
