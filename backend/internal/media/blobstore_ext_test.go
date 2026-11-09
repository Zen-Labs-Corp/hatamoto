package media

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
)

func TestLocalStoreOptionalInterfaces(t *testing.T) {
	ctx := context.Background()
	st, err := NewLocalStore(t.TempDir(), "/media")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a/one.txt", "a/two.txt", "b/three.txt"} {
		if _, err := st.Put(ctx, k, strings.NewReader("hello "+k)); err != nil {
			t.Fatal(err)
		}
	}

	info, err := st.(Stater).Stat(ctx, "a/one.txt")
	if err != nil || info.Size != int64(len("hello a/one.txt")) || info.ETag == "" || info.ModTime.IsZero() {
		t.Fatalf("Stat = %+v, %v", info, err)
	}
	if _, err := st.(Stater).Stat(ctx, "a/none.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat missing = %v", err)
	}

	list, err := st.(Lister).List(ctx, "a/")
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	all, _ := st.(Lister).List(ctx, "")
	if len(all) != 3 {
		t.Fatalf("List all = %d", len(all))
	}

	oi, f, err := st.(Opener).Open(ctx, "b/three.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "hello b/three.txt" || oi.Key != "b/three.txt" {
		t.Fatalf("Open = %q %+v", b, oi)
	}
	if _, _, err := st.(Opener).Open(ctx, "b/none"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open missing = %v", err)
	}
}
