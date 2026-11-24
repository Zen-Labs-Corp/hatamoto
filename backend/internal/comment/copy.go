package comment

// The owner-facing copy for turning comments on (ADR 0005 rule 1). The admin
// settings page, the kit review screen and the CLI all print these.
const (
	EnableWarning        = "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue."
	EnableConfirmWarning = "Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly."
	EnableTick           = "I understand this will invite spam"

	// NoAddressMsg is shown when comments are turned on with no site address.
	NoAddressMsg = "Set your site address in Settings > General before turning comments on."
)
