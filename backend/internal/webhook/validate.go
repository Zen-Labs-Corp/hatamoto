package webhook

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// ValidationError is a problem with a subscription's fields; handlers show it
// as a 422.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// forbiddenHeaders are set by the sender or the transport; a subscription
// cannot override them.
var forbiddenHeaders = []string{"host", "content-type", "content-length", "user-agent", "connection", "transfer-encoding", "upgrade", "te", "trailer", "proxy-authorization", "proxy-connection"}

// ApplyDefaults sets the values of a new subscription.
func (s *Subscription) ApplyDefaults() {
	s.Active = true
	s.MaxRetries, s.RetryDelaySeconds, s.TimeoutSeconds = 3, 60, 5
}

// Validate normalises and checks a subscription. It returns a
// *ValidationError for the first problem.
func (s *Subscription) Validate() error {
	if msg := s.problem(); msg != "" {
		return &ValidationError{Msg: msg}
	}
	return nil
}

func (s *Subscription) problem() string {
	s.Name = strings.TrimSpace(s.Name)
	s.URL = strings.TrimSpace(s.URL)
	if s.Name == "" || len(s.Name) > 120 {
		return "name is required (at most 120 characters)"
	}
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "url must be an https address without credentials"
	}
	if u.Scheme == "http" && !s.AllowHTTP {
		return "url must be https; tick \"allow plain http\" only if you accept that the payload and its signature travel unencrypted"
	}
	if len(s.Events) == 0 {
		return "choose at least one event"
	}
	seen := map[string]bool{}
	for _, e := range s.Events {
		if !slices.Contains(eventTypes, e) {
			return fmt.Sprintf("unknown event %q", e)
		}
		if seen[e] {
			return fmt.Sprintf("event %q is listed twice", e)
		}
		seen[e] = true
	}
	if s.MaxRetries < 0 || s.MaxRetries > 10 {
		return "max_retries must be 0 to 10"
	}
	if s.RetryDelaySeconds < 0 || s.RetryDelaySeconds > 3600 {
		return "retry_delay_seconds must be 0 to 3600"
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Sprintf("timeout_seconds must be 1 to %d", MaxTimeoutSeconds)
	}
	if len(s.Headers) > 20 {
		return "at most 20 custom headers"
	}
	for k, v := range s.Headers {
		if k == "" || strings.ContainsAny(k, " :\r\n") || strings.ContainsAny(v, "\r\n") || slices.Contains(forbiddenHeaders, strings.ToLower(k)) {
			return fmt.Sprintf("header %q is not allowed", k)
		}
	}
	return ""
}
