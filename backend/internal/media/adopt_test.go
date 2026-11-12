package media

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeAdoptRepo struct {
	referenced map[string]struct{}
	created    []*Media
}

func (f *fakeAdoptRepo) ReferencedKeys(context.Context) (map[string]struct{}, error) {
	return f.referenced, nil
}

func (f *fakeAdoptRepo) Create(_ context.Context, m *Media) error {
	f.created = append(f.created, m)
	return nil
}

func adoptFixture(t *testing.T) (Lister, *fakeAdoptRepo) {
	t.Helper()
	st, err := NewLocalStore(t.TempDir(), "/media")
	if err != nil {
		t.Fatal(err)
	}
	tenant := legacyMediaTenantDir
	putKeys(t, st, "logo.png", "2019/04/cover.JPG", "docs/notes.xyz", "wp/x.png", "wp2/y.png",
		tenant+"/known.png", tenant+"/known_thumb.png", tenant+"/staged/r/z.png", ".import-1/q.png")
	repo := &fakeAdoptRepo{referenced: map[string]struct{}{tenant + "/known.png": {}, tenant + "/known_thumb.png": {}}}
	return longNameLister{st.(Lister)}, repo
}

// longNameLister adds a file whose name no filesystem would store.
type longNameLister struct{ Lister }

func (l longNameLister) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	objs, err := l.Lister.List(ctx, prefix)
	return append(objs, ObjectInfo{Key: "long/" + strings.Repeat("n", 256) + ".png", Size: 1}), err
}

func TestAdoptDryRunCreatesNothing(t *testing.T) {
	l, repo := adoptFixture(t)
	res, err := Adopt(context.Background(), repo, l, AdoptOptions{Dir: "/", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, it := range res.Items {
		keys = append(keys, it.Key)
	}
	want := "2019/04/cover.JPG,docs/notes.xyz,logo.png,wp/x.png,wp2/y.png"
	if strings.Join(keys, ",") != want || res.Adopted != 0 || len(repo.created) != 0 {
		t.Errorf("items %v adopted %d created %d, want items %s and nothing created", keys, res.Adopted, len(repo.created), want)
	}
	if res.AlreadyHaveRow != 2 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "file name longer") {
		t.Errorf("already %d skipped %v, want 2 and the long name", res.AlreadyHaveRow, res.Skipped)
	}
}

func TestAdoptApplyBuildsRows(t *testing.T) {
	l, repo := adoptFixture(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	res, err := Adopt(context.Background(), repo, l, AdoptOptions{Dir: "2019", Apply: true, Provider: "s3", Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 || len(repo.created) != 1 {
		t.Fatalf("adopted %d, created %d, want 1", res.Adopted, len(repo.created))
	}
	m := repo.created[0]
	if m.Filename != "cover.JPG" || m.OriginalFilename != "cover.JPG" || m.MimeType != "image/jpeg" || m.FileSize != 1 ||
		m.StorageProvider != "s3" || m.StoragePath != "2019/04/cover.JPG" || m.PublicURL == nil || *m.PublicURL != "/media/2019/04/cover.JPG" ||
		m.Variants == nil || len(m.Variants) != 0 || m.Folder != "/2019/04" || !m.CreatedAt.Equal(at) {
		t.Errorf("row = %+v", m)
	}
	res, err = Adopt(context.Background(), repo, l, AdoptOptions{Dir: "logo.png", Apply: true, Provider: "local"})
	if err != nil || res.Adopted != 1 || repo.created[1].Folder != "/" {
		t.Errorf("single file: %+v %v folder %q", res, err, repo.created[1].Folder)
	}
	docs, err := Adopt(context.Background(), repo, l, AdoptOptions{Dir: "docs", Provider: "local"})
	if err != nil || docs.Items[0].Mime != "application/octet-stream" {
		t.Errorf("unknown extension: %+v %v", docs, err)
	}
}

func TestAdoptRejectsBadDir(t *testing.T) {
	l, repo := adoptFixture(t)
	for _, dir := range []string{"..", "a/../../b", "a\\b", "a\x00b"} {
		if _, err := Adopt(context.Background(), repo, l, AdoptOptions{Dir: dir, Apply: true}); err == nil {
			t.Errorf("Adopt(%q) succeeded, want an error", dir)
		}
	}
	if len(repo.created) != 0 {
		t.Error("a rejected dir created rows")
	}
}
