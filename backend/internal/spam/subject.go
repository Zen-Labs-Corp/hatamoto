package spam

// CommentSubjectPrefix marks comment subjects. Form slugs are [a-z0-9-] only
// (internal/form/service.go), so no form slug can contain ":".
const CommentSubjectPrefix = "comment:"

// CommentSubject is the timing-token subject for comments on one entity.
func CommentSubject(entityID string) string { return CommentSubjectPrefix + entityID }
