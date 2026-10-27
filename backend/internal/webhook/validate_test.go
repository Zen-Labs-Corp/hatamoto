package webhook

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func validSub() Subscription {
	s := Subscription{Name: "n", URL: "https://example.com/h", Events: []string{EventEntityCreated}}
	s.ApplyDefaults()
	return s
}

func TestValidateAccepts(t *testing.T) {
	s := validSub()
	if err := s.Validate(); err != nil {
		t.Fatalf("a valid subscription was refused: %v", err)
	}
	plain := validSub()
	plain.URL, plain.AllowHTTP = "http://example.com/h", true
	if err := plain.Validate(); err != nil {
		t.Fatalf("explicitly allowed http was refused: %v", err)
	}
	edge := validSub()
	edge.Name = strings.Repeat("a", 120)
	edge.MaxRetries, edge.RetryDelaySeconds, edge.TimeoutSeconds = 10, 3600, 1
	edge.Events = ValidEvents()
	edge.Headers = map[string]string{}
	for i := range 20 {
		edge.Headers["X-H"+string(rune('a'+i))] = "v"
	}
	if err := edge.Validate(); err != nil {
		t.Fatalf("boundary values were refused: %v", err)
	}
	trim := validSub()
	trim.Name, trim.URL = "  n  ", "  https://example.com/h  "
	if err := trim.Validate(); err != nil || trim.Name != "n" || trim.URL != "https://example.com/h" {
		t.Fatalf("not trimmed: %+v (%v)", trim, err)
	}
}

func TestValidateMessages(t *testing.T) {
	hdr := func(k, v string) func(*Subscription) {
		return func(s *Subscription) { s.Headers = map[string]string{k: v} }
	}
	tooMany := map[string]string{}
	for i := range 21 {
		tooMany["X-H"+string(rune('a'+i))] = "v"
	}
	cases := []struct {
		name string
		mut  func(*Subscription)
		want string
	}{
		{"empty name", func(s *Subscription) { s.Name = "  " }, "name is required (at most 120 characters)"},
		{"long name", func(s *Subscription) { s.Name = strings.Repeat("a", 121) }, "name is required (at most 120 characters)"},
		{"ftp url", func(s *Subscription) { s.URL = "ftp://example.com" }, "url must be an https address without credentials"},
		{"no host", func(s *Subscription) { s.URL = "https://" }, "url must be an https address without credentials"},
		{"url with login", func(s *Subscription) { s.URL = "https://u:p@example.com" }, "url must be an https address without credentials"},
		{"plain http", func(s *Subscription) { s.URL = "http://example.com/h" }, "url must be https; tick \"allow plain http\" only if you accept that the payload and its signature travel unencrypted"},
		{"no events", func(s *Subscription) { s.Events = nil }, "choose at least one event"},
		{"unknown event", func(s *Subscription) { s.Events = []string{"nope"} }, `unknown event "nope"`},
		{"test event", func(s *Subscription) { s.Events = []string{EventTest} }, `unknown event "webhook.test"`},
		{"duplicate", func(s *Subscription) { s.Events = []string{EventEntityCreated, EventEntityCreated} }, `event "entity.created" is listed twice`},
		{"retries high", func(s *Subscription) { s.MaxRetries = 11 }, "max_retries must be 0 to 10"},
		{"retries low", func(s *Subscription) { s.MaxRetries = -1 }, "max_retries must be 0 to 10"},
		{"delay high", func(s *Subscription) { s.RetryDelaySeconds = 3601 }, "retry_delay_seconds must be 0 to 3600"},
		{"delay low", func(s *Subscription) { s.RetryDelaySeconds = -1 }, "retry_delay_seconds must be 0 to 3600"},
		{"timeout high", func(s *Subscription) { s.TimeoutSeconds = 6 }, "timeout_seconds must be 1 to 5"},
		{"timeout low", func(s *Subscription) { s.TimeoutSeconds = 0 }, "timeout_seconds must be 1 to 5"},
		{"too many headers", func(s *Subscription) { s.Headers = tooMany }, "at most 20 custom headers"},
		{"empty header name", hdr("", "v"), `header "" is not allowed`},
		{"header space", hdr("X A", "v"), `header "X A" is not allowed`},
		{"header colon", hdr("X:A", "v"), `header "X:A" is not allowed`},
		{"header name newline", hdr("X-A\r\nX-B", "v"), "is not allowed"},
		{"header value CR", hdr("X-A", "b\rc"), `header "X-A" is not allowed`},
		{"header value LF", hdr("X-A", "b\nX-B: c"), `header "X-A" is not allowed`},
	}
	for _, h := range forbiddenHeaders {
		cases = append(cases, struct {
			name string
			mut  func(*Subscription)
			want string
		}{"forbidden " + h, hdr(strings.ToUpper(h), "v"), "is not allowed"})
	}
	for _, c := range cases {
		s := validSub()
		c.mut(&s)
		err := s.Validate()
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want *ValidationError", c.name, err)
			continue
		}
		if !strings.Contains(ve.Error(), c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, ve.Error(), c.want)
		}
	}
}

func TestApplyDefaults(t *testing.T) {
	var s Subscription
	s.ApplyDefaults()
	if !s.Active || s.MaxRetries != 3 || s.RetryDelaySeconds != 60 || s.TimeoutSeconds != 5 {
		t.Fatalf("defaults: %+v", s)
	}
}

func TestValidEventsStableAndCopied(t *testing.T) {
	ev := ValidEvents()
	if len(ev) != 6 || ev[0] != EventEntityCreated || ev[5] != EventImportCompleted {
		t.Fatalf("events: %v", ev)
	}
	for _, e := range ev {
		if e == EventTest {
			t.Fatal("webhook.test must not be subscribable")
		}
	}
	ev[0] = "x"
	if ValidEvents()[0] != EventEntityCreated {
		t.Fatal("ValidEvents must return a copy")
	}
}

func TestNewSecret(t *testing.T) {
	a, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSecret()
	if !regexp.MustCompile(`^whsec_[0-9a-f]{64}$`).MatchString(a) || a == b {
		t.Fatalf("secrets: %q %q", a, b)
	}
}
