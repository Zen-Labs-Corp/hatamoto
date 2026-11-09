package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/media"
)

const testMediaSecret = "very-secret-key-value"

func s3Env(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		media.EnvStorage: "s3", media.EnvS3Endpoint: "http://127.0.0.1:9", media.EnvS3Region: "r1",
		media.EnvS3Bucket: "bkt", media.EnvS3Prefix: "sites/one/media/", media.EnvS3AccessKey: "ak", media.EnvS3SecretKey: testMediaSecret,
	} {
		t.Setenv(k, v)
	}
}

// list and delete work on an object storage install with no S3 setting at all:
// they only read the data directory.
func TestBackupListDeleteNeedNoObjectStorageConfig(t *testing.T) {
	data := t.TempDir()
	t.Setenv("HATAMOTO_DATA", data)
	t.Setenv("DATABASE_URL", "")
	t.Setenv(media.EnvStorage, "s3") // and none of HATAMOTO_MEDIA_S3_*
	id := seedBackup(t, data)
	ctx := context.Background()

	var out bytes.Buffer
	if err := runOps(ctx, []string{"backup", "list"}, nil, &out); err != nil || !strings.Contains(out.String(), id) {
		t.Fatalf("list: %v %q", err, out.String())
	}
	out.Reset()
	if err := runOps(ctx, []string{"backup", "delete", id}, nil, &out); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestCreateArgsWithMedia(t *testing.T) {
	ca, err := parseCreateArgs([]string{"--config", "/etc/x.env", "--with-media"})
	if err != nil || ca.configPath != "/etc/x.env" || !ca.withMedia {
		t.Fatalf("parseCreateArgs = %+v, %v", ca, err)
	}
	if ca, err := parseCreateArgs(nil); err != nil || ca.withMedia {
		t.Fatalf("default = %+v, %v", ca, err)
	}
	if _, err := parseCreateArgs([]string{"extra"}); err == nil {
		t.Error("a positional argument must fail")
	}
}

func TestMediaObjectTargetNamesBucketNotSecret(t *testing.T) {
	t.Setenv(media.EnvStorage, "")
	if _, ok := mediaObjectTarget(); ok {
		t.Error("local media reported an object storage target")
	}
	s3Env(t)
	got, ok := mediaObjectTarget()
	if !ok || got != "bkt/sites/one/media/" || strings.Contains(got, testMediaSecret) {
		t.Fatalf("target = %q, %v", got, ok)
	}
}

func TestMediaStoreOrUnavailable(t *testing.T) {
	if mediaStoreOrUnavailable(func(k string) string { return "" }) != nil {
		t.Error("local media must give no store")
	}
	incomplete := func(k string) string { return map[string]string{media.EnvStorage: "s3"}[k] }
	store := mediaStoreOrUnavailable(incomplete)
	if store == nil {
		t.Fatal("invalid settings must not mean local media")
	}
	if _, err := store.Put(context.Background(), "a", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), media.EnvS3Bucket) {
		t.Errorf("unavailable store error = %v", err)
	}
	if _, ok := mediaStoreOrUnavailable(completeS3Env).(*media.S3Store); !ok {
		t.Error("complete settings must give the object storage store")
	}
}

func completeS3Env(k string) string {
	return map[string]string{
		media.EnvStorage: "s3", media.EnvS3Endpoint: "http://127.0.0.1:9", media.EnvS3Region: "r1",
		media.EnvS3Bucket: "bkt", media.EnvS3AccessKey: "ak", media.EnvS3SecretKey: "sk",
	}[k]
}
