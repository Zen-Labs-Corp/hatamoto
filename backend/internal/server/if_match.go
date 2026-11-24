package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

// ifMatch is a parsed If-Match header (RFC 9110 §13.1.1), limited to one
// strong entity tag or "*".
type ifMatch struct {
	present bool // the header was sent
	any     bool // "*": any current representation
	version int  // the tag's version when present && !any
}

var errIfMatch = errors.New(`If-Match must be one strong entity tag such as "12", or *`)

// parseIfMatch reads the If-Match header: absent, "*" or one strong tag
// holding a version number. Weak tags, lists and anything else are an error.
func parseIfMatch(r *http.Request) (ifMatch, error) {
	vals := r.Header.Values("If-Match")
	switch len(vals) {
	case 0:
		return ifMatch{}, nil
	case 1:
	default:
		return ifMatch{}, errIfMatch
	}
	v := strings.TrimSpace(vals[0])
	if v == "*" {
		return ifMatch{present: true, any: true}, nil
	}
	if len(v) < 3 || v[0] != '"' || v[len(v)-1] != '"' {
		return ifMatch{}, errIfMatch
	}
	digits := v[1 : len(v)-1]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return ifMatch{}, errIfMatch
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return ifMatch{}, errIfMatch
	}
	return ifMatch{present: true, version: n}, nil
}

// expected is the version the write must still find, nil when none is named.
func (m ifMatch) expected() *int {
	if !m.strong() {
		return nil
	}
	v := m.version
	return &v
}

// strong reports whether a specific version was named.
func (m ifMatch) strong() bool { return m.present && !m.any }

// setETag sets the entity tag of a content version. Call before writing the body.
func setETag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(version)+`"`)
}

// writeVersionConflict answers a *entity.VersionConflictError with status
// (412 or 409), the current ETag and current_version, and reports whether it did.
func writeVersionConflict(w http.ResponseWriter, err error, status int) bool {
	var vc *entity.VersionConflictError
	if !errors.As(err, &vc) {
		return false
	}
	setETag(w, vc.Current)
	writeJSON(w, status, map[string]any{
		"error":           fmt.Sprintf("entity changed: current version is %d", vc.Current),
		"current_version": vc.Current,
	})
	return true
}
