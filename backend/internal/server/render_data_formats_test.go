package server

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/media"
)

func TestImageVariantsCarryTheFormat(t *testing.T) {
	t.Parallel()
	w := 2000
	raw := []byte(`[{"size":"large","url":"/m/a_large.jpg","width":1024},{"size":"large","format":"image/webp","url":"/m/a_large.webp","width":1024}]`)
	got := imageVariants(raw, "/m/a.jpg", &w)
	var webp, plain int
	for _, v := range got {
		switch v.Type {
		case "image/webp":
			webp++
		case "":
			plain++
		}
	}
	if webp != 1 || plain != 2 { // large jpg + the original
		t.Errorf("imageVariants = %+v", got)
	}
}

func TestRenderDataDropsFormatsSwitchedOff(t *testing.T) {
	t.Parallel()
	w := 2000
	raw := []byte(`[{"size":"large","url":"/m/a_large.jpg","width":1024},` +
		`{"size":"large","format":"image/webp","url":"/m/a_large.webp","width":1024},` +
		`{"size":"large","format":"image/avif","url":"/m/a_large.avif","width":1024}]`)
	list := imageVariants(raw, "/m/a.jpg", &w)
	for _, c := range []struct {
		name     string
		f        media.Formats
		wantType map[string]int
	}{
		{"both on", media.Formats{WebP: true, AVIF: true}, map[string]int{"": 2, "image/webp": 1, "image/avif": 1}},
		{"avif off", media.Formats{WebP: true}, map[string]int{"": 2, "image/webp": 1}},
		{"all off", media.Formats{}, map[string]int{"": 2}},
	} {
		d := &renderData{s: &Server{formatPolicy: func(context.Context) media.Formats { return c.f }}}
		got := map[string]int{}
		for _, v := range d.filterFormats(list) {
			got[v.Type]++
		}
		if len(got) != len(c.wantType) {
			t.Errorf("%s: %v want %v", c.name, got, c.wantType)
			continue
		}
		for k, n := range c.wantType {
			if got[k] != n {
				t.Errorf("%s: %v want %v", c.name, got, c.wantType)
			}
		}
	}
	if len(list) != 4 {
		t.Error("the cached list must not be modified")
	}
}
