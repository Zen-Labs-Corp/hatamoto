package admin

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"hatamoto.dev/core/web"
)

// The htmx config meta turns off everything the strict CSP breaks and swaps
// validation (422) responses in place.
func TestHTMXConfig(t *testing.T) {
	t.Parallel()

	var cfg struct {
		AllowEval              bool `json:"allowEval"`
		AllowScriptTags        bool `json:"allowScriptTags"`
		IncludeIndicatorStyles bool `json:"includeIndicatorStyles"`
		ResponseHandling       []struct {
			Code  string `json:"code"`
			Swap  bool   `json:"swap"`
			Error *bool  `json:"error"`
		} `json:"responseHandling"`
	}
	if err := json.Unmarshal([]byte((&View{Nonce: "n"}).HTMXConfig()), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AllowEval || cfg.AllowScriptTags || cfg.IncludeIndicatorStyles {
		t.Errorf("allowEval, allowScriptTags and includeIndicatorStyles must be false: %+v", cfg)
	}
	found := false
	for _, r := range cfg.ResponseHandling {
		if r.Code == "422" {
			found = r.Swap && r.Error != nil && !*r.Error
		}
	}
	if !found {
		t.Errorf("responseHandling must swap 422 without an error: %+v", cfg.ResponseHandling)
	}
}

// Tiptap's injected <style> is refused by the CSP and loses white-space rules
// silently, so every Editor is built with injectCSS:false (its CSS is in
// css/pages/pageeditor.css).
func TestEveryEditorSetsInjectCSSFalse(t *testing.T) {
	t.Parallel()

	root := web.Admin()
	n := 0
	err := fs.WalkDir(root, "js", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		src := string(b)
		for i := strings.Index(src, "new Editor("); i >= 0; {
			n++
			rest := src[i:]
			end := strings.Index(rest[1:], "new Editor(")
			if end < 0 {
				end = len(rest) - 1
			}
			if !strings.Contains(rest[:end+1], "injectCSS: false") {
				t.Errorf("%s: new Editor without injectCSS: false", p)
			}
			next := strings.Index(rest[1:], "new Editor(")
			if next < 0 {
				break
			}
			i += next + 1
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Fatalf("found %d Editor constructions, expected the kit and prose ones", n)
	}
}
