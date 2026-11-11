package email

import (
	"context"
	"fmt"
	"strings"
)

// RunDigestRow is one automatic run in the daily summary mail. URL is the
// admin page of its changeset, or the path alone when the site has no base URL.
type RunDigestRow struct {
	Plugin  string
	Run     string
	Changes int
	Created int
	URL     string
}

// Updates lists the runs shown in the main list: every run that changed
// existing content, and any run that did nothing else.
func (d RunDigestData) Updates() []RunDigestRow {
	var out []RunDigestRow
	for _, r := range d.Runs {
		if r.Changes > 0 || r.Created == 0 {
			out = append(out, r)
		}
	}
	return out
}

// NewEntries lists the runs that published new entries on their own.
func (d RunDigestData) NewEntries() []RunDigestRow {
	var out []RunDigestRow
	for _, r := range d.Runs {
		if r.Created > 0 {
			out = append(out, r)
		}
	}
	return out
}

// RunDigestData drives run_digest.html.
type RunDigestData struct {
	SiteName   string
	Runs       []RunDigestRow
	HistoryURL string
}

// SendRunDigest mails an owner the runs that published themselves in the last
// day. Like every send it is a no-op when the service is unconfigured.
func (s *Service) SendRunDigest(ctx context.Context, to string, data RunDigestData) error {
	html, err := s.render("run_digest.html", data)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "These runs published their changes on %s by themselves in the last day, inside the rule you allowed.\n\n", data.SiteName)
	for _, r := range data.Updates() {
		noun := "changes"
		if r.Changes == 1 {
			noun = "change"
		}
		fmt.Fprintf(&b, "%s, %s: %d %s\n%s\n\n", r.Plugin, r.Run, r.Changes, noun, r.URL)
	}
	if created := data.NewEntries(); len(created) > 0 {
		b.WriteString("New entries published automatically:\n\n")
		for _, r := range created {
			noun := "new entries"
			if r.Created == 1 {
				noun = "new entry"
			}
			fmt.Fprintf(&b, "%s, %s: %d %s\n%s\n\n", r.Plugin, r.Run, r.Created, noun, r.URL)
		}
	}
	fmt.Fprintf(&b, "See the full history:\n%s\n", data.HistoryURL)
	return s.sendAuth(ctx, KindRunDigest, to, data.SiteName, "Automatic updates on "+data.SiteName, html, b.String())
}
