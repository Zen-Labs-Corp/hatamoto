package agents

import "testing"

func TestClassifyReferral(t *testing.T) {
	reg := Default()
	cases := []struct {
		name, ref, utm, want string
	}{
		{"chatgpt.com", "https://chatgpt.com/", "", "chatgpt"},
		{"chat.openai.com", "https://chat.openai.com/c/x", "", "chatgpt"},
		{"utm only", "", "chatgpt.com", "chatgpt"},
		{"utm uppercase", "", " ChatGPT.com ", "chatgpt"},
		{"google organic", "https://www.google.com/", "", ""},
		{"gemini", "https://gemini.google.com/app", "", "gemini"},
		{"claude", "https://claude.ai/", "", "claude"},
		{"perplexity apex", "https://perplexity.ai/search", "", "perplexity"},
		{"perplexity www", "https://www.perplexity.ai/search?q=x", "", "perplexity"},
		{"copilot", "https://copilot.microsoft.com/", "", "copilot"},
		{"deepseek", "https://chat.deepseek.com/", "", "deepseek"},
		{"meta.ai subdomain", "https://www.meta.ai/", "", "meta_ai"},
		{"lookalike suffix", "https://evilchatgpt.com/", "", ""},
		{"lookalike subdomain-of-evil", "https://chatgpt.com.evil.io/", "", ""},
		{"host with port", "https://chatgpt.com:443/", "", "chatgpt"},
		{"malformed", "://%%%", "", ""},
		{"non-http scheme", "android-app://com.openai.chatgpt", "", ""},
		{"empty", "", "", ""},
		{"utm wins over referrer", "https://chatgpt.com/", "perplexity", "perplexity"},
		{"unknown utm falls back to referrer", "https://claude.ai/", "newsletter", "claude"},
		{"unknown both", "https://example.com/", "newsletter", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := reg.ClassifyReferral(c.ref, c.utm)
			if got != c.want || ok != (c.want != "") {
				t.Fatalf("ClassifyReferral(%q,%q) = %q,%v; want %q", c.ref, c.utm, got, ok, c.want)
			}
		})
	}
}

func TestParseRejectsUppercaseReferrer(t *testing.T) {
	_, err := Parse([]byte(`{"version":1,"ai_referrers":[{"source":"x","hosts":["Chat.com"],"utm":[]}],"agents":[]}`))
	if err == nil {
		t.Fatal("expected error for uppercase ai_referrer host")
	}
}
