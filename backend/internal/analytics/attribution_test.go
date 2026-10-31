package analytics

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/agents"
)

func TestParseAttribution(t *testing.T) {
	reg := agents.Default()
	tests := []struct {
		name, raw string
		ok        bool
		first     *Touch
		last      *Touch
	}{
		{"assistant referrer", `{"f":{"h":"perplexity.ai","p":"/pricing"},"l":{"h":"perplexity.ai","p":"/pricing"}}`, true,
			&Touch{Channel: ChannelAI, Source: "perplexity", Landing: "/pricing"}, &Touch{Channel: ChannelAI, Source: "perplexity", Landing: "/pricing"}},
		{"second arrival keeps first", `{"f":{"h":"perplexity.ai","p":"/a"},"l":{"s":"Newsletter","m":"email","c":"Spring","p":"/b?x=1"}}`, true,
			&Touch{Channel: ChannelAI, Source: "perplexity", Landing: "/a"}, &Touch{Channel: ChannelEmail, Source: "newsletter", Medium: "email", Campaign: "spring", Landing: "/b"}},
		{"paid flag", `{"f":{"h":"google.com","g":true,"p":"/"},"l":null}`, true,
			&Touch{Channel: ChannelPaid, Source: "google.com", Landing: "/"}, nil},
		{"own host is direct", `{"f":{"h":"www.Site.test","p":"/"}}`, true,
			&Touch{Channel: ChannelDirect, Source: SourceNone, Landing: "/"}, nil},
		{"empty touch is direct", `{"l":{}}`, true, nil, &Touch{Channel: ChannelDirect, Source: SourceNone}},
		{"client channel ignored", `{"f":{"h":"google.com","channel":"paid"}}`, true,
			&Touch{Channel: ChannelOrganic, Source: "google.com"}, nil},
		{"no touches", `{}`, false, nil, nil},
		{"empty", ``, false, nil, nil},
		{"invalid json", `{"f":`, false, nil, nil},
		{"wrong types", `{"f":"x"}`, false, nil, nil},
		{"oversize", `{"f":{"h":"` + strings.Repeat("a", 2000) + `"}}`, false, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseAttribution(tc.raw, reg, "site.test:8080")
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !sameTouch(got.First, tc.first) || !sameTouch(got.Last, tc.last) {
				t.Fatalf("got first=%+v last=%+v, want first=%+v last=%+v", got.First, got.Last, tc.first, tc.last)
			}
		})
	}
}

func sameTouch(a, b *Touch) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// TestParseAttributionMatchesTrack pins the lead's channel to the one /track
// gives the same inputs.
func TestParseAttributionMatchesTrack(t *testing.T) {
	reg := agents.Default()
	for _, in := range []rawTouch{
		{Host: "chatgpt.com"}, {Host: "news.ycombinator.com"}, {Host: "mail.google.com"}, {Host: "x.com"},
		{Source: "nl", Medium: "email"}, {Source: "g", Medium: "cpc"}, {Host: "bing.com", Paid: true}, {},
	} {
		ev := Event{Ref: refHost(cleanToken(in.Host, maxRefLen)), UTMSource: in.Source, UTMMedium: in.Medium, Paid: in.Paid}
		wantCh, wantSrc := Classify(ev, reg, "site.test")
		got := classifyTouch(&in, reg, "site.test")
		if wantCh == "" {
			wantCh, wantSrc = ChannelDirect, SourceNone
		}
		if got.Channel != wantCh || got.Source != wantSrc {
			t.Errorf("%+v: got %s/%s, /track gives %s/%s", in, got.Channel, got.Source, wantCh, wantSrc)
		}
	}
}
