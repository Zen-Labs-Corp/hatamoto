// Package agentpolicy lets a wrapper module decide, per API key, whether an
// agent's change to live content is allowed, must wait for a person's review,
// or is refused with a reason. A build that registers nothing behaves as
// before: no key is asked anything.
//
// The policy is asked only for writes made with an API key (REST and MCP)
// that publish, schedule, restore or edit live content. People are governed
// by roles and review, not by this policy.
//
// RequireReview stages the change for the site's reviewers even when the
// site's review switch is off; a change held only by
// the policy has the requester "api-key:<key id>" so the key's creator may
// approve it; one the site's own rule held keeps the creator as requester. A policy's Allow never skips the site's
// own review rule. The zero Decision, an error or a panic refuses the change
// and stages nothing (fail closed). Deny reasons are trimmed, kept to one
// line and capped at 200 characters.
//
// Decide is called on the request path of every such write: cache in the
// policy what is slow.
//
// A wrapper module has its own main package that imports a package whose
// init function calls Register, then calls app.Main:
//
//	package main
//
//	import (
//		_ "example.com/yourmodule/agents" // init calls agentpolicy.Register
//		"hatamoto.dev/core/app"
//	)
//
//	func main() { app.Main() }
//
// Register must run before app.Main (from an init function); app.Main
// freezes the registration and a later Register panics.
//
// Changes lists what a key changed, read from the audit log: only rows
// written for that key, oldest first, at most MaxChanges per call; page by
// calling again with the last row's At and skipping ids already seen. Its
// rows cover entity writes, refusals ("agent_policy.deny") and review
// requests ("changeset.edit_submit"). A change a person approved is
// attributed to that person. Changes works in serve only; elsewhere it
// returns ErrUnavailable.
//
// Policy will not gain methods, and Request and Entity may gain fields: build
// them with keyed literals.
package agentpolicy
