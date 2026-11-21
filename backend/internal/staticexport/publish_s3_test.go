package staticexport

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/s3"
	"hatamoto.dev/core/internal/s3/s3test"
)

func s3Fixture(t *testing.T, prefix string) (*S3Target, *s3test.Server) {
	t.Helper()
	srv := s3test.NewServer(t)
	c, err := s3.New(s3.Config{Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	return NewS3Target(c, srv.URL, s3test.Bucket, prefix), srv
}

func stageFiles(t *testing.T, files map[string]string) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{Version: manifestVersion, Files: map[string]Entry{}}
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		m.Files[p] = Entry{SHA256: "sum-" + p, Size: int64(len(body)), Type: ContentType(p)}
	}
	return dir, m
}

func TestS3Target_UploadsChangedDeletesRemovedLast(t *testing.T) {
	tgt, srv := s3Fixture(t, "site/")
	srv.Put("site/old.html", []byte("old"), nil)
	staged, next := stageFiles(t, map[string]string{"index.html": "<p>hi</p>", "a/index.html": "<p>a</p>"})
	next.Files["index.html"] = Entry{SHA256: "s1", Size: 9, Type: "text/html; charset=utf-8"}
	d := Diff{Changed: []string{"a/index.html", "index.html"}, Removed: []string{"old.html"}}

	if err := tgt.Publish(context.Background(), staged, d, next); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPut || r.Method == http.MethodDelete {
			seq = append(seq, r.Method)
		}
	}
	if got := strings.Join(seq, ","); got != "PUT,PUT,DELETE,PUT" {
		t.Fatalf("request order = %s", got)
	}
	last := srv.Requests()[len(srv.Requests())-1]
	if last.Method != http.MethodPut || !strings.HasSuffix(last.Path, "/site/"+ManifestFile) {
		t.Fatalf("manifest is not the last request: %s %s", last.Method, last.Path)
	}
	objs := srv.Objects()
	if _, ok := objs["site/old.html"]; ok {
		t.Fatal("removed key still stored")
	}
	if objs["site/index.html"].Meta["sha256"] != "s1" {
		t.Fatalf("meta = %v", objs["site/index.html"].Meta)
	}
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPut && strings.HasSuffix(r.Path, "/site/index.html") {
			if got := r.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			if got := r.Header.Get("Cache-Control"); got != "public, max-age=300" {
				t.Fatalf("Cache-Control = %q", got)
			}
		}
	}
}

func TestS3Target_UploadErrorNoDeletes(t *testing.T) {
	tgt, srv := s3Fixture(t, "")
	srv.Put("gone.html", []byte("x"), nil)
	srv.FailPut(func(key string) bool { return key == "b.html" })
	staged, next := stageFiles(t, map[string]string{"a.html": "a", "b.html": "b"})
	d := Diff{Changed: []string{"a.html", "b.html"}, Removed: []string{"gone.html"}}

	if err := tgt.Publish(context.Background(), staged, d, next); err == nil {
		t.Fatal("expected an upload error")
	}
	for _, r := range srv.Requests() {
		if r.Method == http.MethodDelete {
			t.Fatalf("unexpected DELETE %s", r.Path)
		}
	}
	if _, ok := srv.Objects()[ManifestFile]; ok {
		t.Fatal("manifest written after a failed upload")
	}
	if _, ok := srv.Objects()["gone.html"]; !ok {
		t.Fatal("removed key was deleted")
	}
}

func TestS3Target_ModifiedAfterPartialPublish(t *testing.T) {
	staged, next := stageFiles(t, map[string]string{"a.html": "a"})
	d := Diff{Changed: []string{"a.html"}}

	tgt, srv := s3Fixture(t, "")
	srv.FailPut(func(key string) bool { return key == "a.html" })
	if err := tgt.Publish(context.Background(), staged, d, next); err == nil {
		t.Fatal("expected an upload error")
	}
	if tgt.Modified() {
		t.Fatal("Modified before any upload succeeded")
	}

	tgt, srv = s3Fixture(t, "")
	srv.FailPut(func(key string) bool { return key == ManifestFile })
	if err := tgt.Publish(context.Background(), staged, d, next); err == nil {
		t.Fatal("expected a manifest error")
	}
	if !tgt.Modified() {
		t.Fatal("not Modified after an upload succeeded")
	}
}

func TestS3Target_NoPreviousManifestDeletesNothing(t *testing.T) {
	tgt, srv := s3Fixture(t, "p/")
	srv.Put("p/stranger.html", []byte("not ours"), nil)
	prev, ok, err := tgt.Previous(context.Background())
	if err != nil || ok {
		t.Fatalf("Previous = %v, %v", ok, err)
	}
	staged, next := stageFiles(t, map[string]string{"index.html": "x"})
	d := DiffManifests(prev, next)
	if len(d.Removed) != 0 || len(d.Changed) != 1 {
		t.Fatalf("diff = %+v", d)
	}
	if err := tgt.Publish(context.Background(), staged, d, next); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Objects()["p/stranger.html"]; !ok {
		t.Fatal("unrelated object was deleted")
	}
	got, ok, err := tgt.Previous(context.Background())
	if err != nil || !ok || len(got.Files) != 1 {
		t.Fatalf("manifest round trip: %v %v %+v", ok, err, got)
	}
}

func TestS3Target_UnchangedNotUploaded(t *testing.T) {
	tgt, srv := s3Fixture(t, "")
	staged, next := stageFiles(t, map[string]string{"same.html": "s", "new.html": "n"})
	d := Diff{Changed: []string{"new.html"}, Unchanged: []string{"same.html"}}
	if err := tgt.Publish(context.Background(), staged, d, next); err != nil {
		t.Fatal(err)
	}
	for _, r := range srv.Requests() {
		if strings.HasSuffix(r.Path, "/same.html") {
			t.Fatalf("unchanged file was requested: %s %s", r.Method, r.Path)
		}
	}
}
