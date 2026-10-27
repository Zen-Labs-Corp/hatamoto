package agents

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// logpushLine is a Cloudflare Logpush http_requests record (the fields we use).
type logpushLine struct {
	ClientIP  string          `json:"ClientIP"`
	URI       string          `json:"ClientRequestURI"`
	Method    string          `json:"ClientRequestMethod"`
	UserAgent string          `json:"ClientRequestUserAgent"`
	Status    int             `json:"EdgeResponseStatus"`
	Bytes     int64           `json:"EdgeResponseBytes"`
	StartedAt json.RawMessage `json:"EdgeStartTimestamp"`
}

// caddyLine is a Caddy JSON access-log record.
type caddyLine struct {
	TS       json.RawMessage `json:"ts"` // float seconds (default), or a string with time_format
	Status   int             `json:"status"`
	Size     int64           `json:"size"`
	Duration float64         `json:"duration"` // seconds
	Request  struct {
		ClientIP string              `json:"client_ip"`
		RemoteIP string              `json:"remote_ip"`
		Method   string              `json:"method"`
		URI      string              `json:"uri"`
		Headers  map[string][]string `json:"headers"`
	} `json:"request"`
}

// ParseLogLine parses one access-log line (Cloudflare Logpush or Caddy JSON,
// auto-detected) into a RawHit. It reports false for lines that are not
// parseable or that decision 19 (as amended in the P5 review) assigns to
// another capture source: only /media/* lines (any status) are kept, because
// pages, sitemaps, llms.txt and robots.txt, 5xx included, are already recorded
// by the middleware, the backend and the robots route. Query strings are stripped.
func ParseLogLine(line []byte) (RawHit, bool) {
	return parseLogLineAt(line, time.Now())
}

// Log timestamps outside [now-maxLogAge, now+maxLogFuture] are rejected: rows
// land in a table that is never retention-dropped, so a misconfigured
// timestamp_format must not be able to plant years-old or future data.
const (
	maxLogAge    = 7 * 24 * time.Hour
	maxLogFuture = 5 * time.Minute
)

func parseLogLineAt(line []byte, now time.Time) (RawHit, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return RawHit{}, false
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(line, &probe) != nil {
		return RawHit{}, false
	}
	var hit RawHit
	switch {
	case probe["ClientRequestURI"] != nil:
		var l logpushLine
		if json.Unmarshal(line, &l) != nil {
			return RawHit{}, false
		}
		hit = RawHit{
			At: parseLogpushTime(l.StartedAt), UA: l.UserAgent, IP: l.ClientIP,
			Method: l.Method, Path: l.URI, Status: l.Status, Bytes: int(l.Bytes),
		}
	case probe["request"] != nil:
		var l caddyLine
		if json.Unmarshal(line, &l) != nil {
			return RawHit{}, false
		}
		ip := l.Request.ClientIP
		if ip == "" {
			ip = l.Request.RemoteIP
		}
		ua := ""
		if v := l.Request.Headers["User-Agent"]; len(v) > 0 {
			ua = v[0]
		}
		hit = RawHit{
			UA: ua, IP: stripPort(ip), Method: l.Request.Method, Path: l.Request.URI,
			Status: l.Status, Bytes: int(l.Size), DurationMS: int(l.Duration * 1000),
		}
		hit.At = parseCaddyTime(l.TS)
	default:
		return RawHit{}, false
	}
	if i := strings.IndexAny(hit.Path, "?#"); i >= 0 {
		hit.Path = hit.Path[:i]
	}
	if hit.UA == "" || hit.Path == "" || hit.Status < 100 || hit.Status > 599 {
		return RawHit{}, false
	}
	if !strings.HasPrefix(hit.Path, "/media/") {
		return RawHit{}, false
	}
	if !hit.At.IsZero() && (hit.At.Before(now.Add(-maxLogAge)) || hit.At.After(now.Add(maxLogFuture))) {
		return RawHit{}, false
	}
	hit.Source = SourceLog
	return hit, true
}

// parseCaddyTime accepts Caddy's default float seconds, or a string ts
// (RFC3339 or a numeric string, from a custom time_format). Zero means unknown.
func parseCaddyTime(raw json.RawMessage) time.Time {
	s := strings.Trim(string(raw), `"`)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return time.Time{}
	}
	if f >= 1e11 { // ms/us/ns epoch, not seconds
		return parseLogpushTime(json.RawMessage(strconv.FormatInt(int64(f), 10)))
	}
	return time.Unix(0, int64(f*float64(time.Second))).UTC()
}

// parseLogpushTime accepts RFC3339 strings or integer Unix timestamps (s, ms,
// us or ns, told apart by magnitude). Zero time means "now" to the writer.
func parseLogpushTime(raw json.RawMessage) time.Time {
	s := strings.Trim(string(raw), `"`)
	if s == "" || s == "null" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	switch {
	case n >= 1e17:
		return time.Unix(0, n).UTC()
	case n >= 1e14:
		return time.UnixMicro(n).UTC()
	case n >= 1e11:
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}

// stripPort removes a :port suffix from "ip:port" / "[v6]:port"; bare IPs
// (including bare IPv6) pass through.
func stripPort(s string) string {
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return s[1:i]
		}
	}
	if strings.Count(s, ":") == 1 {
		return s[:strings.Index(s, ":")]
	}
	return s
}
