package webhook

import (
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeImported(t *testing.T) {
	good := func() Subscription {
		return Subscription{ID: "a", Name: "n", URL: "https://x.example/h", Secret: "whsec_x", Active: true,
			Events: []string{EventEntityPublished}, MaxRetries: 3, RetryDelaySeconds: 60, TimeoutSeconds: 5}
	}
	many := map[string]string{}
	for i := range 25 {
		many[fmt.Sprintf("X-H%02d", i)] = "v"
	}
	cases := []struct {
		name  string
		edit  func(*Subscription)
		check func(*testing.T, Subscription)
	}{
		{"retries clamped", func(s *Subscription) { s.MaxRetries = 99 }, func(t *testing.T, s Subscription) {
			if s.MaxRetries != 10 {
				t.Fatal(s.MaxRetries)
			}
		}},
		{"delay clamped", func(s *Subscription) { s.RetryDelaySeconds = 99999 }, func(t *testing.T, s Subscription) {
			if s.RetryDelaySeconds != 3600 {
				t.Fatal(s.RetryDelaySeconds)
			}
		}},
		{"timeout raised", func(s *Subscription) { s.TimeoutSeconds = 0 }, func(t *testing.T, s Subscription) {
			if s.TimeoutSeconds != 1 {
				t.Fatal(s.TimeoutSeconds)
			}
		}},
		{"timeout lowered", func(s *Subscription) { s.TimeoutSeconds = 60 }, func(t *testing.T, s Subscription) {
			if s.TimeoutSeconds != MaxTimeoutSeconds {
				t.Fatal(s.TimeoutSeconds)
			}
		}},
		{"junk and repeated events dropped", func(s *Subscription) {
			s.Events = []string{"nope", EventEntityDeleted, EventEntityDeleted, "form.updated"}
		}, func(t *testing.T, s Subscription) {
			if len(s.Events) != 1 || s.Events[0] != EventEntityDeleted || !s.Active {
				t.Fatal(s.Events, s.Active)
			}
		}},
		{"25 headers become 20", func(s *Subscription) { s.Headers = many }, func(t *testing.T, s Subscription) {
			if len(s.Headers) != 20 {
				t.Fatal(len(s.Headers))
			}
		}},
		{"owned and malformed headers dropped", func(s *Subscription) {
			s.Headers = map[string]string{"Host": "x", "Content-Type": "x", "Bad Name": "x", "X-Ok": "1", "X-Nl": "a\nb"}
		}, func(t *testing.T, s Subscription) {
			if len(s.Headers) != 1 || s.Headers["X-Ok"] != "1" {
				t.Fatal(s.Headers)
			}
		}},
		{"empty name falls back to the url", func(s *Subscription) { s.Name = "  " }, func(t *testing.T, s Subscription) {
			if s.Name != s.URL {
				t.Fatal(s.Name)
			}
		}},
		{"long name cut", func(s *Subscription) { s.Name = strings.Repeat("n", 300) }, func(t *testing.T, s Subscription) {
			if len(s.Name) != 120 || !s.Active {
				t.Fatal(len(s.Name), s.Active)
			}
		}},
		{"http url is flagged and kept active", func(s *Subscription) { s.URL = "http://x.example/h" }, func(t *testing.T, s Subscription) {
			if !s.AllowHTTP || !s.Active {
				t.Fatal(s.AllowHTTP, s.Active)
			}
		}},
		{"https url is not flagged", func(s *Subscription) { s.AllowHTTP = true }, func(t *testing.T, s Subscription) {
			if s.AllowHTTP {
				t.Fatal("flagged")
			}
		}},
		{"no events pauses", func(s *Subscription) { s.Events = nil }, func(t *testing.T, s Subscription) {
			if s.Active || len(s.Events) != 0 {
				t.Fatal(s.Active, s.Events)
			}
		}},
		{"unusable url pauses", func(s *Subscription) { s.URL = "ftp://x" }, func(t *testing.T, s Subscription) {
			if s.Active {
				t.Fatal("still active")
			}
		}},
		{"id and secret kept", func(s *Subscription) {}, func(t *testing.T, s Subscription) {
			if s.ID != "a" || s.Secret != "whsec_x" || !s.Active {
				t.Fatal(s)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := good()
			c.edit(&s)
			SanitizeImported(&s)
			c.check(t, s)
		})
	}
}
