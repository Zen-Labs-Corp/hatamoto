package searchreplace

import (
	"reflect"
	"testing"
)

func all(int) bool { return true }

func TestTransformModes(t *testing.T) {
	cases := []struct {
		name string
		s    string
		o    Options
		want string
		n    int
	}{
		{"plain matches inside words", "cat concat Cat", Options{Find: "cat", Replace: "dog"}, "dog condog dog", 3},
		{"whole word", "cat concat cat_x cat.", Options{Find: "cat", Replace: "dog", WholeWord: true}, "dog concat cat_x dog.", 2},
		{"match case", "cat Cat", Options{Find: "cat", Replace: "x", MatchCase: true}, "x Cat", 1},
		{"unicode fold", "Ärger äRGER", Options{Find: "ärger", Replace: "x"}, "x x", 2},
		{"empty find", "abc", Options{Find: ""}, "abc", 0},
		{"url", "see old.example/shop now", Options{Find: "old.example/shop", Replace: "new.example/store"}, "see new.example/store now", 1},
	}
	for _, c := range cases {
		got, hits, n := transform(c.s, c.o, all)
		if got != c.want || n != c.n || len(hits) != c.n {
			t.Errorf("%s: got %q n=%d hits=%d, want %q n=%d", c.name, got, n, len(hits), c.want, c.n)
		}
	}
}

func TestTransformCollectOnly(t *testing.T) {
	out, hits, n := transform("one two one", Options{Find: "one", Replace: "x"}, nil)
	if out != "one two one" || n != 0 || len(hits) != 2 || hits[1].Index != 1 {
		t.Fatalf("out=%q n=%d hits=%v", out, n, hits)
	}
	if hits[0].Context != "one two one" {
		t.Fatalf("context %q", hits[0].Context)
	}
}

func TestTransformPickSecond(t *testing.T) {
	got, _, n := transform("a a a", Options{Find: "a", Replace: "b"}, func(i int) bool { return i == 1 })
	if got != "a b a" || n != 1 {
		t.Fatalf("got %q n=%d", got, n)
	}
	got, _, n = transform(`<p>a</p><p>a <b>a</b></p>`, Options{Find: "a", Replace: "b"}, func(i int) bool { return i == 1 })
	if got != `<p>a</p><p>b <b>a</b></p>` || n != 1 {
		t.Fatalf("html: got %q n=%d", got, n)
	}
}

func TestTransformMarkup(t *testing.T) {
	o := Options{Find: "shop", Replace: "store"}
	cases := []struct{ name, in, want string }{
		{"text only, tags and classes kept", `<p class="shop">my shop</p>`, `<p class="shop">my store</p>`},
		{"entity in text", `<p>tea &amp; shop</p>`, `<p>tea &amp; store</p>`},
		{"href", `<a title="shop" href="https://x.example/shop">go</a>`, `<a title="shop" href="https://x.example/store">go</a>`},
		{"src", `<img src="/shop.png" alt="shop">`, `<img src="/store.png" alt="shop">`},
		{"nested", `<div><ul><li><em>shop</em></li></ul></div>`, `<div><ul><li><em>store</em></li></ul></div>`},
		{"script and style untouched", `<style>.shop{}</style><script>var shop=1</script><p>shop</p>`, `<style>.shop{}</style><script>var shop=1</script><p>store</p>`},
		{"unmodified markup is byte identical", `<P CLASS=x>none<br/></P>`, `<P CLASS=x>none<br/></P>`},
	}
	for _, c := range cases {
		got, _, _ := transform(c.in, o, all)
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestTransformMarkupHits(t *testing.T) {
	_, hits, _ := transform(`<p>a shop here</p><a href="/shop">x</a>`, Options{Find: "shop"}, nil)
	want := []Hit{{0, "a shop here"}, {1, "/shop"}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits %v want %v", hits, want)
	}
}
