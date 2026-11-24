package comment

// The owner-facing copy for turning comments on (ADR 0005 rule 1). The admin
// settings page, the kit review screen and the CLI all print these.
const (
	EnableWarning        = "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue."
	EnableConfirmWarning = "Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly."
	EnableTick           = "I understand this will invite spam"

	// NoAddressMsg is shown when comments are turned on with no site address.
	NoAddressMsg = "Set your site address in Settings > General before turning comments on."

	// The community policy per content type (ADR 0005 9.5). Settings > Comments >
	// Content types, the kit review screen and the CLI print these.
	PolicyOnWarning      = "Links and first-time commenters are posted at once, not held."
	PolicyConfirmWarning = "Are you sure? With the community policy on, comments with links and first comments on this content type are posted at once, without review. Spam checks, blocked commenters, duplicates, captcha and surge mode still hold comments."
	PolicyTick           = "I understand comments on this type will not wait for review"
)
