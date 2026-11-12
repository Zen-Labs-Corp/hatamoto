package media

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func putKeys(t *testing.T, st interface {
	Put(context.Context, string, io.Reader) (int64, error)
}, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, err := st.Put(context.Background(), k, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIgnoredKey(t *testing.T) {
	tenant := legacyMediaTenantDir
	for key, want := range map[string]bool{
		"a/b.png":                     false,
		tenant + "/x.png":             false,
		tenant + "/staged/run/x.png":  true,
		tenant + "/replace-1.tmp":     true,
		"other/replace-1.tmp":         false,
		tenant + "/replace-1.tmp.old": true,
		"other/replace-1.tmp.old":     false,
		tenant + "/2026/hero_thumb.webp.2f1c6b0e-8d0b-4c64-9a52-6a1d7c0e5b11.old": true,
		tenant + "/hero_thumb.webp.2f1c6b0e-8d0b-4c64-9a52-6a1d7c0e5b11.old":      true,
		tenant + "/hero.webp.old": false,
		".import-1/x.png":         true,
		"a/.hidden":               true,
		"wpimport-123/x.png":      true,
		"a\\b.png":                true,
		"a\x00b.png":              true,
		"a/\xffb.png":             true,
		"staged/x.png":            false,
		"":                        true,
		"a/b/":                    true,
		tenant + "/":              true,
	} {
		if got := IgnoredKey(key); got != want {
			t.Errorf("IgnoredKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestCleanKeyPrefix(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", ".": "", "wp": "wp", "/wp/": "wp", "wp//2020/./01": "wp/2020/01", "/abs/dir": "abs/dir"} {
		got, err := CleanKeyPrefix(in)
		if err != nil || got != want {
			t.Errorf("CleanKeyPrefix(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"..", "../x", "a/../b", "a\\b", "a\x00b", "/../x"} {
		if got, err := CleanKeyPrefix(in); err == nil {
			t.Errorf("CleanKeyPrefix(%q) = %q, want an error", in, got)
		}
	}
}

func TestRowlessLocal(t *testing.T) {
	st, err := NewLocalStore(t.TempDir(), "/media")
	if err != nil {
		t.Fatal(err)
	}
	tenant := legacyMediaTenantDir
	putKeys(t, st, tenant+"/a.jpg", tenant+"/a_thumb.jpg", tenant+"/loose.png", "wp/x.png", "wp2/y.png",
		tenant+"/staged/r/z.png", tenant+"/replace-1.tmp", ".import-1/q.png", "dir/.hidden")
	ref := map[string]struct{}{tenant + "/a.jpg": {}, tenant + "/a_thumb.jpg": {}}
	res, err := Rowless(context.Background(), st.(Lister), ref, RowlessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{tenant + "/loose.png", "wp/x.png", "wp2/y.png"}
	if res.Count != 3 || res.Bytes != 3 || strings.Join(res.Sample, ",") != strings.Join(want, ",") {
		t.Errorf("result = %+v, want the keys %v", res, want)
	}
	res, err = Rowless(context.Background(), st.(Lister), ref, RowlessOptions{Prefix: "wp"})
	if err != nil || res.Count != 1 || res.Sample[0] != "wp/x.png" {
		t.Errorf("prefix wp = %+v, %v; want only wp/x.png", res, err)
	}
}

func TestRowlessSampleCapped(t *testing.T) {
	st, fake := newTestS3Store(t, "site/")
	_ = fake
	for i := 0; i < 5; i++ {
		putKeys(t, st, fmt.Sprintf("k/%02d.png", i))
	}
	res, err := Rowless(context.Background(), st, nil, RowlessOptions{SampleMax: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 5 || len(res.Sample) != 2 || res.Sample[0] != "k/00.png" || res.Sample[1] != "k/01.png" {
		t.Errorf("result = %+v, want count 5 and the first two keys", res)
	}
}
