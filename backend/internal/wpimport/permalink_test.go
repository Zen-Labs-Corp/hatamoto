package wpimport

import (
	"testing"
	"time"
)

func TestInferAndConvertPermalink(t *testing.T) {
	d := time.Date(2024, 5, 10, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct{ link, slug, id, structure, pattern string }{
		{"https://x.test/2024/05/hello/", "hello", "7", "/%year%/%monthnum%/%postname%/", "/{year}/{month}/{slug}"},
		{"https://x.test/2024/05/10/hello/", "hello", "7", "/%year%/%monthnum%/%day%/%postname%/", "/{year}/{month}/{day}/{slug}"},
		{"https://x.test/hello/", "hello", "7", "/%postname%/", "/{slug}"},
		{"https://x.test/blog/hello", "hello", "7", "/blog/%postname%", "/blog/{slug}"},
		{"https://x.test/archives/7", "7", "7", "/archives/%postname%", "/archives/{slug}"},
		{"https://x.test/?p=7", "hello", "7", "", ""},
	} {
		got := InferPermalink(c.link, c.slug, c.id, d)
		if got != c.structure {
			t.Errorf("InferPermalink(%s) = %q, want %q", c.link, got, c.structure)
		}
		if p, _ := PatternFromPermalink(got); p != c.pattern {
			t.Errorf("PatternFromPermalink(%q) = %q, want %q", got, p, c.pattern)
		}
	}
	if _, ok := PatternFromPermalink("/%category%/%postname%/"); ok {
		t.Error("category structure has no pattern")
	}
	if _, ok := PatternFromPermalink("/archives/%post_id%"); ok {
		t.Error("post id structure has no pattern")
	}
}
