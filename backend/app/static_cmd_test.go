package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/staticexport"
)

func TestParseStaticArgs(t *testing.T) {
	cases := []struct {
		args    []string
		want    staticArgs
		wantErr bool
	}{
		{[]string{"--out", "/srv/x"}, staticArgs{out: "/srv/x", target: "dir"}, false},
		{[]string{"--target", "s3"}, staticArgs{target: "s3"}, false},
		{[]string{"--target", "dir"}, staticArgs{target: "dir"}, false},
		{nil, staticArgs{}, false},
		{[]string{"--target", "ftp"}, staticArgs{}, true},
		{[]string{"--out", "x", "--target", "s3"}, staticArgs{}, true},
		{[]string{"positional"}, staticArgs{}, true},
		// A relative --out parses; the directory guard refuses it later.
		{[]string{"--out", "rel/dir"}, staticArgs{out: "rel/dir", target: "dir"}, false},
	}
	for _, c := range cases {
		got, err := parseStaticArgs(c.args)
		if (err != nil) != c.wantErr || (err == nil && got != c.want) {
			t.Errorf("parseStaticArgs(%v) = %+v, %v", c.args, got, err)
		}
	}
}

func TestStaticUsageListed(t *testing.T) {
	if !strings.Contains(usage, "static export") {
		t.Fatal("the usage text does not list `static export`")
	}
}

func noSettings(context.Context) (staticexport.Settings, bool, error) {
	return staticexport.Defaults(), false, nil
}

func TestResolveStaticTarget(t *testing.T) {
	ctx := context.Background()
	noEnv := func(string) string { return "" }
	secret := func() (string, error) { return "server-secret", nil }

	t.Run("no target", func(t *testing.T) {
		_, err := resolveStaticTarget(ctx, staticArgs{}, staticTargetDeps{getenv: noEnv, settings: noSettings, secret: secret})
		if err == nil || !strings.Contains(err.Error(), "no target") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("out dir", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "site")
		tgt, err := resolveStaticTarget(ctx, staticArgs{out: out, target: "dir"}, staticTargetDeps{getenv: noEnv, settings: noSettings, secret: secret})
		if err != nil || tgt.Describe() != out {
			t.Fatalf("target = %v, err = %v", tgt, err)
		}
	})

	t.Run("relative out refused", func(t *testing.T) {
		if _, err := resolveStaticTarget(ctx, staticArgs{out: "rel", target: "dir"}, staticTargetDeps{getenv: noEnv, settings: noSettings, secret: secret}); err == nil {
			t.Fatal("a relative directory was accepted")
		}
	})

	t.Run("data dir refused", func(t *testing.T) {
		data := t.TempDir()
		_, err := resolveStaticTarget(ctx, staticArgs{out: data, target: "dir"}, staticTargetDeps{getenv: noEnv, settings: noSettings, secret: secret, forbidden: []string{data}})
		if err == nil {
			t.Fatal("the data directory was accepted")
		}
	})

	t.Run("s3 from env", func(t *testing.T) {
		env := map[string]string{
			staticexport.EnvBucket: "my-site", staticexport.EnvEndpoint: "https://s3.example.com", staticexport.EnvRegion: "us-east-1",
			staticexport.EnvAccessKey: "AKIAEXAMPLE", staticexport.EnvSecretKey: "topsecret", staticexport.EnvPrefix: "site",
		}
		tgt, err := resolveStaticTarget(ctx, staticArgs{target: "s3"}, staticTargetDeps{getenv: func(k string) string { return env[k] }, settings: noSettings, secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		if tgt.Describe() != "s3://my-site/site/" || strings.Contains(tgt.Key(), "topsecret") {
			t.Fatalf("describe = %q key = %q", tgt.Describe(), tgt.Key())
		}
	})

	t.Run("s3 overlapping media refused", func(t *testing.T) {
		env := map[string]string{
			staticexport.EnvBucket: "my-site", staticexport.EnvEndpoint: "https://s3.example.com", staticexport.EnvRegion: "us-east-1",
			staticexport.EnvAccessKey: "AKIAEXAMPLE", staticexport.EnvSecretKey: "topsecret",
			"MEDIA_STORAGE": "s3", "HATAMOTO_MEDIA_S3_ENDPOINT": "https://s3.example.com", "HATAMOTO_MEDIA_S3_REGION": "us-east-1",
			"HATAMOTO_MEDIA_S3_BUCKET": "my-site", "HATAMOTO_MEDIA_S3_ACCESS_KEY": "AKIAEXAMPLE", "HATAMOTO_MEDIA_S3_SECRET_KEY": "topsecret",
		}
		_, err := resolveStaticTarget(ctx, staticArgs{target: "s3"}, staticTargetDeps{getenv: func(k string) string { return env[k] }, settings: noSettings, secret: secret})
		if err == nil || !strings.Contains(err.Error(), "media") {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(err.Error(), "topsecret") {
			t.Fatal("error leaks the secret")
		}
	})

	t.Run("s3 target above or beside the media prefix refused", func(t *testing.T) {
		for _, tc := range []struct{ mediaPrefix, staticPrefix string }{
			{"media/", ""}, {"media/", "media"}, {"site/media/", "site/"},
		} {
			env := map[string]string{
				staticexport.EnvBucket: "my-site", staticexport.EnvEndpoint: "https://s3.example.com", staticexport.EnvRegion: "us-east-1",
				staticexport.EnvAccessKey: "AKIAEXAMPLE", staticexport.EnvSecretKey: "topsecret", staticexport.EnvPrefix: tc.staticPrefix,
				"MEDIA_STORAGE": "s3", "HATAMOTO_MEDIA_S3_ENDPOINT": "https://s3.example.com", "HATAMOTO_MEDIA_S3_REGION": "us-east-1",
				"HATAMOTO_MEDIA_S3_BUCKET": "my-site", "HATAMOTO_MEDIA_S3_ACCESS_KEY": "AKIAEXAMPLE", "HATAMOTO_MEDIA_S3_SECRET_KEY": "topsecret",
				"HATAMOTO_MEDIA_S3_PREFIX": tc.mediaPrefix,
			}
			_, err := resolveStaticTarget(ctx, staticArgs{target: "s3"}, staticTargetDeps{getenv: func(k string) string { return env[k] }, settings: noSettings, secret: secret})
			if err == nil || !strings.Contains(err.Error(), "media") {
				t.Errorf("media %q static %q: err = %v", tc.mediaPrefix, tc.staticPrefix, err)
			}
		}
	})

	t.Run("saved dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "saved")
		saved := func(context.Context) (staticexport.Settings, bool, error) {
			return staticexport.Settings{Mode: staticexport.ModeStatic, Target: staticexport.TargetDir, DirPath: dir}, true, nil
		}
		tgt, err := resolveStaticTarget(ctx, staticArgs{}, staticTargetDeps{getenv: noEnv, settings: saved, secret: secret})
		if err != nil || tgt.Describe() != dir {
			t.Fatalf("target = %v, err = %v", tgt, err)
		}
	})

	t.Run("saved s3 with a changed server secret", func(t *testing.T) {
		sealed, err := staticexport.SealSecret(staticexport.SecretKey("old-secret"), "topsecret")
		if err != nil {
			t.Fatal(err)
		}
		saved := func(context.Context) (staticexport.Settings, bool, error) {
			return staticexport.Settings{Mode: staticexport.ModeStatic, Target: staticexport.TargetS3, Service: "minio", Endpoint: "https://s3.example.com",
				Region: "us-east-1", Bucket: "my-site", AccessKey: "AKIAEXAMPLE", SecretSealed: sealed}, true, nil
		}
		_, err = resolveStaticTarget(ctx, staticArgs{}, staticTargetDeps{getenv: noEnv, settings: saved, secret: secret})
		if err == nil || !strings.Contains(err.Error(), "HATAMOTO_SECRET") || strings.Contains(err.Error(), "topsecret") {
			t.Fatalf("err = %v", err)
		}
	})
}
