package admin

import (
	"context"
	"net/http"
	"time"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzuser"
	"hatamoto.dev/core/internal/email"
)

// ownerEmails lists the address of every user who may receive owner mail,
// paging through the user list.
func (a *Admin) ownerEmails(ctx context.Context) ([]string, error) {
	us, err := authzuser.Allowed(ctx, a.logger(), a.deps.Manager.List, authz.ActionSiteReceiveOwnerMail, nil)
	return authzuser.Emails(us), err
}

// notifyOwnersAddressChanged emails every owner the new admin address, in the
// background, so a slow or broken mail server never holds up or fails the
// change. Without mail configured nothing is sent. Failures are logged
// without the address.
func (a *Admin) notifyOwnersAddressChanged(r *http.Request, path, by, ip string) {
	if !a.mailConfigured() || a.deps.Manager == nil {
		return
	}
	address := a.siteAddress(r) + path
	when := fmtUTC(time.Now())
	ctx := context.WithoutCancel(r.Context())
	a.notifyWG.Add(1)
	go func() {
		defer a.notifyWG.Done()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		owners, err := a.ownerEmails(ctx)
		if err != nil {
			a.logger().Error("admin address mail: listing owners failed", "err", err)
		}
		data := email.AdminAddressChangedData{SiteName: a.siteName(ctx), Address: address, ChangedBy: by, When: when, IP: ip}
		for _, to := range owners {
			if err := a.deps.Mailer.SendAdminAddressChanged(ctx, to, data); err != nil {
				a.logger().Warn("admin address mail not sent", "to", to, "err", err)
			}
		}
	}()
}
