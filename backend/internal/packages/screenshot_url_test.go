package packages

import "testing"

func TestScreenshotURL(t *testing.T) {
	for _, c := range []struct{ base, rel, want string }{
		{"https://hatamoto.dev/registry.json", "screenshots/a.webp", "https://hatamoto.dev/screenshots/a.webp"},
		{"https://hatamoto.dev/registry/index.json", "screenshots/a.webp", "https://hatamoto.dev/registry/screenshots/a.webp"},
		{"http://hatamoto.dev/registry.json", "screenshots/a.webp", ""},
		{"https://hatamoto.dev/registry.json", "", ""},
		{"https://hatamoto.dev/registry.json", "//evil.example/a.png", "https://hatamoto.dev//evil.example/a.png"},
		{"https://hatamoto.dev/registry.json", "https://evil.example/a.png", "https://hatamoto.dev/https://evil.example/a.png"},
		{"https://hatamoto.dev/registry.json", "../../a.png", "https://hatamoto.dev/a.png"},
		{"%zz", "a.png", ""},
		{"", "a.png", ""},
	} {
		if got := ScreenshotURL(c.base, c.rel); got != c.want {
			t.Errorf("ScreenshotURL(%q, %q) = %q, want %q", c.base, c.rel, got, c.want)
		}
	}
}
