package server

import (
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/render"
)

func TestViewImageURLs(t *testing.T) {
	items := func(imgs ...string) []render.RelatedItem {
		var out []render.RelatedItem
		for _, im := range imgs {
			out = append(out, render.RelatedItem{Image: im})
		}
		return out
	}
	many := make([]string, 12)
	want := []string{"/e.jpg"}
	for i := range many {
		many[i] = "/i" + string(rune('a'+i)) + ".jpg"
		want = append(want, many[i])
	}
	for _, c := range []struct {
		name string
		v    *render.PageView
		want []string
	}{
		{"nil view", nil, nil},
		{"no items", &render.PageView{}, nil},
		{"one item", &render.PageView{Items: items("/a.jpg")}, []string{"/a.jpg"}},
		{"entity and 12 items", &render.PageView{Entity: &render.EntityView{Image: "/e.jpg"}, Items: items(many...)}, want},
		{"items without images", &render.PageView{Entity: &render.EntityView{}, Items: items("", "")}, nil},
		{"mixed", &render.PageView{Items: items("", "/b.jpg", "")}, []string{"/b.jpg"}},
	} {
		if got := viewImageURLs(c.v); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
