package email

import (
	"context"
	"fmt"
)

// StaticExportFailedData drives static_export_failed.html. It carries no target
// configuration, bucket, key or secret: only the first error the engine reported.
type StaticExportFailedData struct {
	SiteName   string
	When       string // formatted by the caller in the site zone
	Error      string // the first error, safe to show
	HostingURL string // <base><admin>/settings/hosting
}

// SendStaticExportFailed tells an owner that the first export of a failure
// streak failed. There is no unsubscribe link: it is a site alarm.
func (s *Service) SendStaticExportFailed(ctx context.Context, to string, d StaticExportFailedData) error {
	site := d.SiteName
	d.SiteName = notifyText(d.SiteName)
	d.When = notifyText(d.When)
	d.Error = notifyText(d.Error)
	html, err := s.render("static_export_failed.html", d)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("The static export of %s at %s failed: %s\nYour previous export is still live.\n\nOpen Settings > Hosting: %s\n", d.SiteName, d.When, d.Error, d.HostingURL)
	subject := headerOr("Static export failed on "+site, "Static export failed")
	return s.sendComment(ctx, KindStaticExportFailed, to, site, subject, html, text, "")
}
