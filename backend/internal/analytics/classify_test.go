package analytics

import (
	"testing"

	"hatamoto.dev/core/internal/agents"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name        string
		ev          Event
		wantChannel string
		wantSource  string
	}{
		{"utm email", Event{UTMSource: "newsletter", UTMMedium: "email", UTMCampaign: "x"}, ChannelEmail, "newsletter"},
		{"chatgpt referrer", Event{Ref: "chatgpt.com"}, ChannelAI, "chatgpt"},
		{"utm_source chatgpt.com", Event{UTMSource: "chatgpt.com"}, ChannelAI, "chatgpt"},
		{"gclid flag", Event{Ref: "google.com", Paid: true}, ChannelPaid, "google.com"},
		{"medium cpc", Event{UTMSource: "ads", UTMMedium: "cpc"}, ChannelPaid, "ads"},
		{"medium paid_social", Event{UTMMedium: "paid_social"}, ChannelPaid, SourceNone},
		{"utm over referrer", Event{Ref: "google.com", UTMSource: "digest", UTMMedium: "email"}, ChannelEmail, "digest"},
		{"medium social", Event{UTMSource: "li", UTMMedium: "social"}, ChannelSocial, "li"},
		{"medium organic", Event{UTMSource: "x", UTMMedium: "organic"}, ChannelOrganic, "x"},
		{"search host", Event{Ref: "www.google.de"}, ChannelOrganic, "google.de"},
		{"social host", Event{Ref: "l.facebook.com"}, ChannelSocial, "l.facebook.com"},
		{"webmail before search", Event{Ref: "mail.google.com"}, ChannelEmail, "mail.google.com"},
		{"unknown host", Event{Ref: "blog.example.org"}, ChannelReferral, "blog.example.org"},
		{"lookalike of social", Event{Ref: "notfacebook.com"}, ChannelReferral, "notfacebook.com"},
		{"unmatched utm_source", Event{UTMSource: "partner"}, ChannelReferral, "partner"},
		{"search lookalike", Event{Ref: "google.evil.com"}, ChannelReferral, "google.evil.com"},
		{"search co.uk", Event{Ref: "www.google.co.uk"}, ChannelOrganic, "google.co.uk"},
		{"nothing", Event{}, ChannelDirect, SourceNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch, src := Classify(tc.ev, agents.Default(), "site.test")
			if ch != tc.wantChannel || src != tc.wantSource {
				t.Fatalf("Classify = (%q, %q), want (%q, %q)", ch, src, tc.wantChannel, tc.wantSource)
			}
		})
	}
}

func TestClassifyOwnHostIsInternal(t *testing.T) {
	for _, ref := range []string{"site.test", "www.site.test", "SITE.test."} {
		if ch, src := Classify(Event{Ref: ref}, agents.Default(), "Site.Test:8090"); ch != "" || src != "" {
			t.Fatalf("ref %q: got (%q, %q), want internal", ref, ch, src)
		}
	}
}
