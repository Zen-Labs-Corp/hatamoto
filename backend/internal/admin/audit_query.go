package admin

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Audit row sources: which stored table a row came from. They are the row id
// prefix and the cursor tie-break.
const (
	auditSrcAdmin = "a" // hatamoto_admin_audit
	auditSrcAgent = "m" // hatamoto_mcp_audit
)

const (
	auditQueryMaxRunes = 100
	auditCursorMaxLen  = 64
)

// auditSources are the values of the Source filter and their labels.
var auditSources = []struct{ Value, Label string }{
	{"admin", "Admin"}, {"api", "API"}, {"agent", "Agent"}, {"cli", "CLI"}, {"system", "System"},
}

// AuditQuery filters the unified audit log (admin actions and agent calls).
// Actor is "u:<uuid>" (a signed-in user), "k:<uuid>" (an API key) or
// "n:<name>" (an actor recorded by name only). Source is one of auditSources.
// From is inclusive and To exclusive; a zero time leaves that end open.
type AuditQuery struct {
	Q, Actor, Source, Action string
	From, To                 time.Time
	Before                   *AuditCursor
	Limit                    int
}

// AuditCursor is the keyset position of the last row of a page: rows after it
// sort strictly older by (At, Src, ID).
type AuditCursor struct {
	At  time.Time
	Src string // auditSrcAdmin or auditSrcAgent
	ID  int64
}

// AuditPage is one page of rows, newest first. Next is set when older rows
// exist.
type AuditPage struct {
	Rows []AuditRow
	Next *AuditCursor
}

// AuditActor is one choice of the Actor filter.
type AuditActor struct{ Value, Label string }

// AuditFacets are the Actor and Action choices seen in a date window.
type AuditFacets struct {
	Actors  []AuditActor
	Actions []string
}

// Encode renders the cursor as an opaque URL-safe token.
func (c AuditCursor) Encode() string {
	raw := strconv.FormatInt(c.At.UnixMicro(), 36) + "." + c.Src + "." + strconv.FormatInt(c.ID, 36)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeAuditCursor parses a token from Encode. Anything malformed or out of
// range yields nil, which callers treat as the first page.
func decodeAuditCursor(s string) *AuditCursor {
	if s == "" || len(s) > auditCursorMaxLen {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 || (parts[1] != auditSrcAdmin && parts[1] != auditSrcAgent) {
		return nil
	}
	micro, err := strconv.ParseInt(parts[0], 36, 64)
	if err != nil {
		return nil
	}
	id, err := strconv.ParseInt(parts[2], 36, 64)
	if err != nil || id <= 0 {
		return nil
	}
	at := time.UnixMicro(micro).UTC()
	if at.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || at.After(time.Now().Add(24*time.Hour)) {
		return nil
	}
	return &AuditCursor{At: at, Src: parts[1], ID: id}
}

// escapeLike makes q match literally inside an ILIKE pattern that uses `\` as
// its escape character.
func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

// normalizeAuditQuery trims and caps the search text and drops a Source or
// Actor that is not one of the accepted shapes.
func normalizeAuditQuery(q AuditQuery) AuditQuery {
	q.Q = strings.TrimSpace(q.Q)
	if r := []rune(q.Q); len(r) > auditQueryMaxRunes {
		q.Q = string(r[:auditQueryMaxRunes])
	}
	if !validAuditSource(q.Source) {
		q.Source = ""
	}
	if !validAuditActor(q.Actor) {
		q.Actor = ""
	}
	return q
}

func validAuditSource(s string) bool {
	for _, o := range auditSources {
		if o.Value == s {
			return true
		}
	}
	return false
}

func validAuditActor(s string) bool {
	kind, val, ok := strings.Cut(s, ":")
	if !ok || val == "" {
		return false
	}
	switch kind {
	case "u", "k":
		_, err := uuid.Parse(val)
		return err == nil
	case "n":
		return true
	}
	return false
}
