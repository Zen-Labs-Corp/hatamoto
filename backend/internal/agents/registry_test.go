package agents

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	reg := Default()
	cases := []struct {
		ua, id, operator string
		purpose          Purpose
	}{
		{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.1; +https://openai.com/gptbot", "gptbot", "OpenAI", PurposeTraining},
		{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot", "chatgpt-user", "OpenAI", PurposeUserFetch},
		{"Mozilla/5.0 (compatible; OAI-SearchBot/1.0; +https://openai.com/searchbot)", "oai-searchbot", "OpenAI", PurposeSearchIndex},
		{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)", "claudebot", "Anthropic", PurposeTraining},
		{"Mozilla/5.0 (compatible; Claude-User/1.0; +Claude-User@anthropic.com)", "claude-user", "Anthropic", PurposeUserFetch},
		{"Mozilla/5.0 (compatible; Claude-SearchBot/1.0; +Claude-SearchBot@anthropic.com)", "claude-searchbot", "Anthropic", PurposeSearchIndex},
		{"anthropic-ai", "claudebot", "Anthropic", PurposeTraining},
		{"Mozilla/5.0 (compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)", "perplexitybot", "Perplexity", PurposeSearchIndex},
		{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Perplexity-User/1.0; +https://perplexity.ai/perplexity-user)", "perplexity-user", "Perplexity", PurposeUserFetch},
		{"CCBot/2.0 (https://commoncrawl.org/faq/)", "ccbot", "Common Crawl", PurposeTraining},
		{"meta-externalagent/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)", "meta-externalagent", "Meta", PurposeTraining},
		{"meta-externalfetcher/1.1", "meta-externalfetcher", "Meta", PurposeUserFetch},
		{"Mozilla/5.0 (Linux; Android 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36 (compatible; Bytespider; spider-feedback@bytedance.com)", "bytespider", "ByteDance", PurposeTraining},
		{"Mozilla/5.0 (compatible; DuckAssistBot/1.2; +http://duckduckgo.com/duckassistbot.html)", "duckassistbot", "DuckDuckGo", PurposeUserFetch},
		{"DuckDuckBot/1.1; (+http://duckduckgo.com/duckduckbot.html)", "duckduckbot", "DuckDuckGo", PurposeSearch},
		{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; MistralAI-User/1.0; +https://docs.mistral.ai/robots", "mistralai-user", "Mistral", PurposeUserFetch},
		{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "googlebot", "Google", PurposeSearch},
		{"GoogleOther", "googleother", "Google", PurposeOtherAI},
		{"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)", "bingbot", "Microsoft", PurposeSearch},
		{"Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)", "ahrefsbot", "Ahrefs", PurposeSEO},
		{"Mozilla/5.0 (compatible; Amazonbot/0.1; +https://developer.amazon.com/support/amazonbot)", "amazonbot", "Amazon", PurposeTraining},
		{"facebookexternalhit/1.1", "facebookexternalhit", "Meta", PurposeSocial},
		{"Mozilla/5.0 (compatible; FacebookBot/1.0; +https://developers.facebook.com/docs/sharing/webmasters/facebookbot/)", "facebookbot", "Meta", PurposeTraining},
		{"ia_archiver", "internet-archive", "Internet Archive", PurposeArchive},
	}
	for _, c := range cases {
		t.Run(c.id+"/"+c.ua[:min(len(c.ua), 24)], func(t *testing.T) {
			a := reg.Match(c.ua)
			if a == nil {
				t.Fatalf("no match for %q", c.ua)
			}
			if a.ID != c.id || a.Operator != c.operator || a.Purpose != c.purpose {
				t.Fatalf("got %s/%s/%s, want %s/%s/%s", a.ID, a.Operator, a.Purpose, c.id, c.operator, c.purpose)
			}
		})
	}
}

func TestMatchNegative(t *testing.T) {
	reg := Default()
	for _, ua := range []string{
		"",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1",
		"node-fetch/1.0",              // generic hint only, not a known agent
		"Mozilla/5.0 Google-Extended", // robots token, never a UA
	} {
		if a := reg.Match(ua); a != nil {
			t.Errorf("Match(%q) = %s, want nil", ua, a.ID)
		}
	}
	if !reg.LooksLikeBot("node-fetch/1.0") || reg.LooksLikeBot("Mozilla/5.0 Chrome/124 Safari/537.36") {
		t.Error("LooksLikeBot hint behaviour wrong")
	}
}

func TestGoogleExtendedIsNotAUAPattern(t *testing.T) {
	reg := Default()
	var tokenFound bool
	for _, a := range reg.Agents {
		for _, p := range a.UA {
			if strings.Contains(p, "google-extended") || strings.Contains(p, "applebot-extended") {
				t.Errorf("%s has UA pattern %q", a.ID, p)
			}
		}
		if a.RobotsToken == "Google-Extended" {
			tokenFound = true
			if len(a.UA) != 0 {
				t.Errorf("token-only entry %s must have no ua", a.ID)
			}
		}
	}
	if !tokenFound {
		t.Error("Google-Extended robots token missing")
	}
}

func TestOrdering(t *testing.T) {
	reg := Default()
	for ua, want := range map[string]string{
		"ChatGPT-User/1.0 GPTBot":   "chatgpt-user",
		"Claude-SearchBot/1.0":      "claude-searchbot",
		"DuckAssistBot DuckDuckBot": "duckassistbot",
		"Claude-User ClaudeBot":     "claude-user",
	} {
		if a := reg.Match(ua); a == nil || a.ID != want {
			t.Errorf("Match(%q) = %v, want %s", ua, a, want)
		}
	}
}

func TestUAHintRegexp(t *testing.T) {
	re := Default().UAHintRegexp()
	for _, ua := range []string{"GPTBot/1.1", "Mozilla Claude-User", "my-crawler", "Screaming Frog SEO"} {
		if !re.MatchString(ua) {
			t.Errorf("hint should match %q", ua)
		}
	}
	if re.MatchString("Mozilla/5.0 (X11; Linux x86_64) Firefox/120.0") {
		t.Error("hint matched a browser UA")
	}
}

func TestParseValidation(t *testing.T) {
	base := func(agent string) []byte {
		return []byte(`{"version":1,"ip_range_sources":{"k":{"urls":["u"],"format":"prefixes"}},"agents":[` + agent + `]}`)
	}
	good := `{"id":"a","purpose":"training","ua":["a"],"ip_ranges":["k"]}`
	if _, err := Parse(base(good)); err != nil {
		t.Fatalf("good registry rejected: %v", err)
	}
	for name, agent := range map[string]string{
		"bad purpose":   `{"id":"a","purpose":"nope","ua":["a"]}`,
		"bad id":        `{"id":"A_b","purpose":"seo","ua":["a"]}`,
		"dup id":        good + `,` + good,
		"uppercase ua":  `{"id":"a","purpose":"seo","ua":["GPT"]}`,
		"unknown range": `{"id":"a","purpose":"seo","ua":["a"],"ip_ranges":["zz"]}`,
	} {
		if _, err := Parse(base(agent)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := Parse([]byte("{")); err == nil {
		t.Error("garbage should error")
	}
}

func TestDefaultLoads(t *testing.T) {
	r := Default()
	if r.Version != 2 || len(r.AIReferrers) == 0 {
		t.Fatalf("unexpected registry: version=%d referrers=%d", r.Version, len(r.AIReferrers))
	}
	if r.ByID("gptbot") == nil {
		t.Error("ByID(gptbot) nil")
	}
}

func TestParseRejectsBadRDNSSuffix(t *testing.T) {
	for _, s := range []string{"semrush.com", ".Semrush.com", ""} {
		data := `{"ip_range_sources":{},"agents":[{"id":"x","purpose":"training","rdns":["` + s + `"]}]}`
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("rdns %q accepted, want error", s)
		}
	}
	if _, err := Parse([]byte(`{"ip_range_sources":{},"agents":[{"id":"x","purpose":"training","rdns":[".semrush.com"]}]}`)); err != nil {
		t.Errorf("valid suffix rejected: %v", err)
	}
}

func TestGoogleRDNSHasNoGoogleusercontent(t *testing.T) {
	for _, id := range []string{"googlebot", "googleother"} {
		for _, s := range Default().ByID(id).RDNS {
			if strings.Contains(s, "googleusercontent") {
				t.Errorf("%s rdns %q lets any GCE VM verify", id, s)
			}
		}
	}
}
