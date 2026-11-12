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

// A file younger than OlderThan may be in flight, written before its row: it
// is counted, not adopted.
func TestAdoptSkipsFilesTooNew(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	l := fixedLister{
		{Key: "old.png", Size: 1, ModTime: now.Add(-48 * time.Hour)},
		{Key: "fresh.png", Size: 1, ModTime: now.Add(-time.Minute)},
		{Key: "unknown.png", Size: 1},
	}
	repo := &fakeAdoptRepo{referenced: map[string]struct{}{}}
	res, err := Adopt(context.Background(), repo, l, AdoptOptions{Apply: true, Provider: "local", OlderThan: 24 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 || res.TooNew != 2 || len(repo.created) != 1 || repo.created[0].StoragePath != "old.png" {
		t.Errorf("adopted %d, too new %d, created %d; want 1, 2, only old.png", res.Adopted, res.TooNew, len(repo.created))
	}
}

// A row that appears between the first load and the insert (an import that
// commits meanwhile) must not get a second row.
func TestAdoptRechecksRowsBeforeInsert(t *testing.T) {
	l := fixedLister{{Key: "a.png", Size: 1}, {Key: "b.png", Size: 1}}
	repo := &laterRowRepo{fakeAdoptRepo: fakeAdoptRepo{referenced: map[string]struct{}{}}}
	res, err := Adopt(context.Background(), repo, l, AdoptOptions{Apply: true, Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 1 || len(repo.created) != 1 || repo.created[0].StoragePath != "b.png" {
		t.Errorf("adopted %d rows %v, want only b.png", res.Adopted, len(repo.created))
	}
}

type fixedLister []ObjectInfo

func (f fixedLister) List(context.Context, string) ([]ObjectInfo, error) { return f, nil }

// laterRowRepo reports a.png as referenced from the second load on.
type laterRowRepo struct {
	fakeAdoptRepo
	loads int
}

func (r *laterRowRepo) ReferencedKeys(context.Context) (map[string]struct{}, error) {
	r.loads++
	if r.loads > 1 {
		return map[string]struct{}{"a.png": {}}, nil
	}
	return map[string]struct{}{}, nil
}

// A key with control or bidi characters is never adopted, and is only ever
// reported escaped.
func TestAdoptSkipsKeysWithControlCharacters(t *testing.T) {
	l := fixedLister{{Key: "a\nb.png", Size: 1}, {Key: "esc\x1b[2J.png", Size: 1}, {Key: "rtl\u202e.png", Size: 1}, {Key: "ok.png", Size: 1}, {Key: "c1\u0085.png", Size: 1}, {Key: "iso\u2067.png", Size: 1}, {Key: "mark\u200e.png", Size: 1},
		{Key: "zwnj\u200c.png", Size: 1}, {Key: "zwj\u200d.png", Size: 1}, {Key: "soft\u00ad.png", Size: 1}}
	repo := &fakeAdoptRepo{referenced: map[string]struct{}{}}
	res, err := Adopt(context.Background(), repo, l, AdoptOptions{Apply: true, Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Adopted != 4 || len(repo.created) != 4 || len(res.Skipped) != 6 {
		t.Fatalf("adopted %d, skipped %v, want 4 adopted (ok, zwnj, zwj, soft hyphen) and 6 skipped", res.Adopted, res.Skipped)
	}
	for _, s := range res.Skipped {
		if strings.ContainsAny(s, "\n\x1b\u202e\u0085\u2067\u200e") {
			t.Errorf("skip reason %q holds a raw control character", s)
		}
	}
}

func TestDisplayKey(t *testing.T) {
	for in, want := range map[string]string{
		"a/b c.png": "a/b c.png",
		"é/ü.png":   "é/ü.png",
		"x\ny":      `"x\ny"`,
		"x\x1b[2Jy": `"x\x1b[2Jy"`,
		"x\u202ey":  `"x\u202ey"`,
		"x\u2066y":  `"x\u2066y"`,
		"x\u200fy":  `"x\u200fy"`,
		"x\u0085y":  `"x\u0085y"`,
		"x\u200cy":  "x\u200cy",
		"x\u200dy":  "x\u200dy",
		"x\u00ady":  "x\u00ady",
	} {
		if got := DisplayKey(in); got != want {
			t.Errorf("DisplayKey(%q) = %s, want %s", in, got, want)
		}
	}
}

// The public URL comes from the store's URL builder, as for uploads, and keys
// that cannot build a valid URL are skipped and reported.
func TestAdoptUsesStoreURLAndSkipsUnbuildableKeys(t *testing.T) {
	l := fixedLister{{Key: "a/b.png", Size: 1}, {Key: "q?x.png", Size: 1}, {Key: "h#x.png", Size: 1}, {Key: "/lead.png", Size: 1}}
	repo := &fakeAdoptRepo{referenced: map[string]struct{}{}}
	res, err := Adopt(context.Background(), repo, l, AdoptOptions{Apply: true, Provider: "s3", URL: func(k string) string { return "https://cdn.example/" + k }})
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.created) != 1 || *repo.created[0].PublicURL != "https://cdn.example/a/b.png" || len(res.Skipped) != 3 {
		t.Errorf("created %d, skipped %v, want 1 row with the store URL and 3 skipped", len(repo.created), res.Skipped)
	}
}
