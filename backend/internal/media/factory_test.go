package media

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/s3/s3test"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestConfigFromServerLocalHasNoStore(t *testing.T) {
	for _, storage := range []string{"", "local"} {
		c, err := ConfigFromServer(config.ServerConfig{MediaUploadDir: "/data/m", MediaBaseURL: "/m", MediaMaxSizeMB: 3},
			envOf(map[string]string{EnvStorage: storage}), nil)
		if err != nil {
			t.Fatal(err)
		}
		if c.Store != nil || c.UploadDir != "/data/m" || c.BaseURL != "/m" || c.MaxSizeMB != 3 {
			t.Errorf("storage %q: config = %+v", storage, c)
		}
	}
}

// An unreachable bucket must not stop the start: the store is built without a
// network call and only fails on use.
func TestConfigFromServerUnreachableBucketStarts(t *testing.T) {
	dead := httptest.NewServer(nil)
	endpoint := dead.URL
	dead.Close()
	c, err := ConfigFromServer(config.ServerConfig{}, envOf(map[string]string{
		EnvStorage: "s3", EnvS3Endpoint: endpoint, EnvS3Region: "r", EnvS3Bucket: "b",
		EnvS3AccessKey: "ak", EnvS3SecretKey: "sk",
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Store.(*S3Store); !ok {
		t.Fatalf("store = %T", c.Store)
	}
	if _, err := c.Store.Put(context.Background(), "a.txt", strings.NewReader("x")); err == nil {
		t.Error("Put to a dead endpoint must fail")
	}
}

func TestConfigFromServerS3UsesTheTarget(t *testing.T) {
	fake := s3test.NewServer(t)
	c, err := ConfigFromServer(config.ServerConfig{}, envOf(map[string]string{
		EnvStorage: "s3", EnvS3Endpoint: fake.URL, EnvS3Region: s3test.Region, EnvS3Bucket: s3test.Bucket,
		EnvS3Prefix: "p/", EnvS3AccessKey: s3test.AccessKey, EnvS3SecretKey: s3test.SecretKey,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.Put(context.Background(), "a.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.Objects()["p/a.txt"]; !ok {
		t.Errorf("objects = %v", fake.Objects())
	}
}

// Invalid settings are an error, and the returned Config never falls back to
// local disk: its store fails every call.
func TestConfigFromServerInvalidFailsClosed(t *testing.T) {
	c, err := ConfigFromServer(config.ServerConfig{}, envOf(map[string]string{EnvStorage: "s3"}), nil)
	if err == nil || !strings.Contains(err.Error(), EnvS3Bucket) {
		t.Fatalf("err = %v", err)
	}
	if c.Store == nil {
		t.Fatal("Store must not be nil (nil means local disk)")
	}
	if _, perr := c.Store.Put(context.Background(), "a.txt", strings.NewReader("x")); perr == nil {
		t.Error("Put on an unavailable store must fail")
	}
	if _, err := ConfigFromServer(config.ServerConfig{}, envOf(map[string]string{EnvStorage: "ftp"}), nil); err == nil {
		t.Error("unknown driver must fail")
	}
}
