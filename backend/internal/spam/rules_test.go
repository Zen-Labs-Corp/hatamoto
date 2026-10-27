package spam

import "testing"

func TestEvaluateRules(t *testing.T) {
	base := Defaults()
	base.BlockedWords = []string{"Crypto"}
	base.BlockedScripts = []string{"cyrillic", "cjk"}

	cases := []struct {
		name   string
		mut    func(*Settings)
		data   map[string]interface{}
		reason string
	}{
		{"clean", nil, map[string]interface{}{"message": "Hello there, see example"}, ""},
		{"two links ok", nil, map[string]interface{}{"m": "http://a.com and www.b.com"}, ""},
		{"scheme plus www is one link", func(s *Settings) { s.MaxLinks = 1 }, map[string]interface{}{"m": "https://www.x.com"}, ""},
		{"www inside a word is no link", func(s *Settings) { s.MaxLinks = 0 }, map[string]interface{}{"m": "Awww. thanks"}, ""},
		{"two scheme plus www links", func(s *Settings) { s.MaxLinks = 1 }, map[string]interface{}{"m": "see https://www.a.com and https://www.b.org"}, ReasonLinks},
		{"five links", nil, map[string]interface{}{"m": "http://a http://b http://c", "n": "https://d www.e"}, ReasonLinks},
		{"links across list", nil, map[string]interface{}{"m": []interface{}{"http://a", "http://b", "http://c"}}, ReasonLinks},
		{"zero blocks any link", func(s *Settings) { s.MaxLinks = 0 }, map[string]interface{}{"m": "visit www.x.com"}, ReasonLinks},
		{"blocked word case insensitive", nil, map[string]interface{}{"m": "Best CRYPTO deals"}, ReasonWord},
		{"blocked word in string list", nil, map[string]interface{}{"m": []string{"fine", "crypto"}}, ReasonWord},
		{"word boundary: ass vs class", func(s *Settings) { s.BlockedWords = []string{"ass"} }, map[string]interface{}{"m": "Our class passed"}, ""},
		{"word boundary: whole word hits", func(s *Settings) { s.BlockedWords = []string{"ass"} }, map[string]interface{}{"m": "you ass."}, ReasonWord},
		{"word boundary: seo vs Seoul", func(s *Settings) { s.BlockedWords = []string{"seo"} }, map[string]interface{}{"m": "Hi from Seoul", "e": "seonaid@example.com"}, ""},
		{"entry starting with dot", func(s *Settings) { s.BlockedWords = []string{".ru"} }, map[string]interface{}{"m": "write me at x.ru"}, ReasonWord},
		{"entry starting with at", func(s *Settings) { s.BlockedWords = []string{"@mail.ru"} }, map[string]interface{}{"m": "spam@mail.ru"}, ReasonWord},
		{"phrase as whole words", func(s *Settings) { s.BlockedWords = []string{"seo services"} }, map[string]interface{}{"m": "Cheap SEO services!"}, ReasonWord},
		{"phrase not inside longer words", func(s *Settings) { s.BlockedWords = []string{"seo services"} }, map[string]interface{}{"m": "Seoul services"}, ""},
		{"unicode letters are word characters", func(s *Settings) { s.BlockedWords = []string{"cafe"} }, map[string]interface{}{"m": "café"}, ""},
		{"cjk word stays substring", func(s *Settings) { s.BlockedWords = []string{"赌博"}; s.BlockedScripts = nil }, map[string]interface{}{"m": "网络赌博网站"}, ReasonWord},
		{"cyrillic", nil, map[string]interface{}{"m": "Привет"}, ReasonScript},
		{"cjk", nil, map[string]interface{}{"m": "你好"}, ReasonScript},
		{"script not selected", func(s *Settings) { s.BlockedScripts = []string{"thai"} }, map[string]interface{}{"m": "Привет"}, ""},
		{"nested map", nil, map[string]interface{}{"m": map[string]interface{}{"a": "Привет"}}, ReasonScript},
		{"non-strings ignored", nil, map[string]interface{}{"n": 5, "b": true}, ""},
		{"no words configured", func(s *Settings) { s.BlockedWords = nil }, map[string]interface{}{"m": "crypto"}, ""},
	}
	for _, c := range cases {
		s := base
		if c.mut != nil {
			c.mut(&s)
		}
		reason, tripped := EvaluateRules(s, c.data)
		if reason != c.reason || tripped != (c.reason != "") {
			t.Errorf("%s: got %q,%v want %q", c.name, reason, tripped, c.reason)
		}
	}
}

func TestLinkCount(t *testing.T) {
	cases := map[string]int{
		"https://www.x.com": 1,
		"Awww.":             0,
		"[url=http://a][/url][url=http://b][/url][url=http://c][/url]": 3,
		"http://a,http://b,http://c":                                   3,
		"www.a.com www.b.com":                                          2,
	}
	for in, want := range cases {
		if got := len(linkRe.FindAllStringIndex(in, -1)); got != want {
			t.Errorf("%q: got %d links, want %d", in, got, want)
		}
	}
}
