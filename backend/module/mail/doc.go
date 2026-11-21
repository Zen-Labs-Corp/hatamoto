// Package mail lets a wrapper module send plain mail through core.
//
//	err := mail.ToOwners(ctx, "reports", mail.Message{
//		Subject: "Weekly report ready",
//		Text:    "The weekly report is ready.",
//	})
//
// module is the module's own name ([a-z][a-z0-9_]{0,30}); it is recorded with
// each mail. Subject (one line, at most 255 characters) and Text are required;
// HTML is optional.
//
// Mail goes through core's mail path: it is sent and written to the email log
// like core mail, with the log kind "module" and the body not kept. When mail
// is not configured, is disabled, or the site runs in staging mode
// (HATAMOTO_ENV=staging), the mail is dropped and logged as suppressed, and
// the call returns nil. ToOwners sends one mail per active owner and joins the
// per-recipient failures. Outside the serve process the calls return
// ErrNoMailer.
//
// Core writes no audit row for module mail; call audit.Record yourself when
// the send should be audited.
package mail
