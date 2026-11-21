package staticexport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedTarget_Dir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "site")
	set := Defaults()
	set.DirPath = dir
	tg, err := SavedTarget(set, SecretKey("test-secret"), DirGuard{}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if tg.Key() != "dir:"+dir {
		t.Fatalf("key = %q", tg.Key())
	}

	forbidden := t.TempDir()
	set.DirPath = filepath.Join(forbidden, "inner")
	if _, err := SavedTarget(set, nil, DirGuard{Forbidden: []string{forbidden}}, os.Getenv); err == nil {
		t.Fatal("a directory inside a forbidden one must be refused")
	}

	set.DirPath = ""
	if _, err := SavedTarget(set, nil, DirGuard{}, os.Getenv); err == nil {
		t.Fatal("no directory saved must fail")
	}
}

func TestSavedTarget_S3(t *testing.T) {
	const plain = "very-secret-value"
	key := SecretKey("test-secret")
	set := validS3()
	set.SecretSealed = nil
	if _, err := SavedTarget(set, key, DirGuard{}, func(string) string { return "" }); err == nil ||
		!strings.Contains(err.Error(), "S3 secret key is not saved") {
		t.Fatalf("no secret: %v", err)
	}

	sealed, err := SealSecret(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	set.SecretSealed = sealed
	if tg, err := SavedTarget(set, key, DirGuard{}, func(string) string { return "" }); err != nil || tg == nil {
		t.Fatalf("saved s3: %v", err)
	}

	env := map[string]string{
		"MEDIA_STORAGE": "s3", "HATAMOTO_MEDIA_S3_ENDPOINT": set.Endpoint, "HATAMOTO_MEDIA_S3_REGION": set.Region,
		"HATAMOTO_MEDIA_S3_BUCKET": set.Bucket, "HATAMOTO_MEDIA_S3_ACCESS_KEY": "AKIAEXAMPLE", "HATAMOTO_MEDIA_S3_SECRET_KEY": "other",
	}
	_, err = SavedTarget(set, key, DirGuard{}, func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "media") {
		t.Fatalf("overlap: %v", err)
	}
	if strings.Contains(err.Error(), plain) {
		t.Fatal("error leaks the secret")
	}

	_, err = SavedTarget(set, SecretKey("another-secret"), DirGuard{}, nil)
	if err == nil || strings.Contains(err.Error(), plain) {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestSavedTarget_UnknownTarget(t *testing.T) {
	set := Defaults()
	set.Target = "ftp"
	if _, err := SavedTarget(set, nil, DirGuard{}, nil); err == nil {
		t.Fatal("unknown target must fail")
	}
}
