package media

import (
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func fullS3Env() map[string]string {
	return map[string]string{
		EnvS3Endpoint: "https://objects.example.com", EnvS3Region: "r1", EnvS3Bucket: "b",
		EnvS3Prefix: "site-a/", EnvS3AccessKey: "ak", EnvS3SecretKey: "sk",
	}
}

func TestStorageFromEnv(t *testing.T) {
	for _, tc := range []struct {
		val, want string
		err       bool
	}{
		{"", "local", false}, {"local", "local", false}, {" S3 ", "s3", false}, {"gcs", "", true},
	} {
		got, err := StorageFromEnv(envMap(map[string]string{EnvStorage: tc.val}))
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("%q: got %q, %v", tc.val, got, err)
		}
	}
}

func TestS3TargetFromEnv(t *testing.T) {
	got, err := S3TargetFromEnv(envMap(fullS3Env()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Prefix != "site-a/" || got.Bucket != "b" || got.SecretKey != "sk" {
		t.Errorf("target = %+v", got)
	}

	env := fullS3Env()
	delete(env, EnvS3Endpoint)
	got, err = S3TargetFromEnv(envMap(env))
	if err != nil || got.Endpoint != "https://s3.r1.amazonaws.com" {
		t.Errorf("aws fallback: %+v, %v", got, err)
	}

	env = fullS3Env()
	delete(env, EnvS3Bucket)
	delete(env, EnvS3SecretKey)
	_, err = S3TargetFromEnv(envMap(env))
	if err == nil || !strings.Contains(err.Error(), EnvS3Bucket) || !strings.Contains(err.Error(), EnvS3SecretKey) {
		t.Errorf("missing vars error = %v", err)
	}

	env = fullS3Env()
	env[EnvS3Endpoint] = "http://objects.example.com"
	if _, err = S3TargetFromEnv(envMap(env)); err == nil {
		t.Error("plain http to a public host must be refused")
	}
	if err != nil && strings.Contains(err.Error(), "sk") && strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks: %v", err)
	}
}
