package auditlog

import "strings"

// redacted replaces the value of a denylisted detail key.
const redacted = "[redacted]"

// denylist holds the lower-case detail keys that must never reach the audit
// log (exact, case-insensitive match on top-level keys).
var denylist = map[string]struct{}{
	"password": {}, "passwd": {}, "secret": {}, "token": {},
	"access_token": {}, "refresh_token": {}, "api_key": {}, "apikey": {},
	"authorization": {}, "cookie": {}, "private_key": {}, "client_secret": {},
}

// Redact returns detail with the value of every denylisted key replaced.
// It never mutates its argument: the map is copied only when a key matches.
func Redact(detail map[string]any) map[string]any {
	var out map[string]any
	for k := range detail {
		if _, hit := denylist[strings.ToLower(k)]; !hit {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(detail))
			for k2, v := range detail {
				out[k2] = v
			}
		}
		out[k] = redacted
	}
	if out == nil {
		return detail
	}
	return out
}
