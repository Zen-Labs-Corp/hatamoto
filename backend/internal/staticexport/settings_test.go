package staticexport

import (
	"strings"
	"testing"
)

func validS3() Settings {
	return Settings{Mode: ModeStatic, Target: TargetS3, Service: "minio", Endpoint: "https://s3.example.com",
		Region: "us-east-1", Bucket: "my-site", AccessKey: "AKIAEXAMPLE", SecretSealed: []byte("sealed")}
}

func TestSettingsDefaultsServed(t *testing.T) {
	d := Defaults()
	if d.Mode != ModeServed || d.Target != TargetDir {
		t.Fatalf("defaults = %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsValidate(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Settings)
		bad  string
	}{
		{"valid s3", func(*Settings) {}, ""},
		{"bad mode", func(s *Settings) { s.Mode = "live" }, "unknown mode"},
		{"bad target", func(s *Settings) { s.Target = "ftp" }, "unknown target"},
		{"relative dir", func(s *Settings) { s.DirPath = "site" }, "absolute"},
		{"static dir needs path", func(s *Settings) { s.Target = TargetDir; s.DirPath = "" }, "directory is required"},
		{"static dir ok", func(s *Settings) { s.Target = TargetDir; s.DirPath = "/srv/site-static" }, ""},
		{"s3 no secret", func(s *Settings) { s.SecretSealed = nil }, "secret key is required"},
		{"s3 bad bucket", func(s *Settings) { s.Bucket = "X" }, "bucket"},
		{"s3 bad service", func(s *Settings) { s.Service = "nope" }, "unknown service"},
		{"served ignores s3 fields", func(s *Settings) { s.Mode = ModeServed; s.Bucket = "" }, ""},
	}
	for _, c := range cases {
		s := validS3()
		c.mod(&s)
		err := s.Validate()
		switch {
		case c.bad == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
			t.Errorf("%s: err = %v, want %q", c.name, err, c.bad)
		}
	}
}

func TestSealOpenSecret(t *testing.T) {
	key := SecretKey("server-secret")
	sealed, err := SealSecret(key, "very-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	s := Settings{SecretSealed: sealed}
	if got, ok := s.Secret(key); !ok || got != "very-secret-value" {
		t.Fatalf("Secret = %q, %v", got, ok)
	}
	if _, ok := s.Secret(SecretKey("other-secret")); ok {
		t.Fatal("a wrong server secret opened the key")
	}
}

func TestS3TargetErrorHasNoSecret(t *testing.T) {
	sealed, err := SealSecret(SecretKey("old-secret"), "very-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	s := validS3()
	s.SecretSealed = sealed
	_, err = s.S3Target(SecretKey("new-secret"))
	if err == nil || !strings.Contains(err.Error(), "HATAMOTO_SECRET") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "very-secret-value") {
		t.Fatal("error leaks the secret")
	}
	tgt, err := func() (string, error) {
		s.SecretSealed, _ = SealSecret(SecretKey("new-secret"), "very-secret-value")
		x, err := s.S3Target(SecretKey("new-secret"))
		return x.SecretKey, err
	}()
	if err != nil || tgt != "very-secret-value" {
		t.Fatalf("open: %q, %v", tgt, err)
	}
}

func TestS3TargetFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if _, ok, err := S3TargetFromEnv(get); ok || err != nil {
		t.Fatalf("unset bucket: ok=%v err=%v", ok, err)
	}
	env[EnvBucket] = "my-site"
	_, ok, err := S3TargetFromEnv(get)
	if !ok || err == nil || !strings.Contains(err.Error(), EnvRegion) || !strings.Contains(err.Error(), EnvSecretKey) {
		t.Fatalf("incomplete: ok=%v err=%v", ok, err)
	}
	env[EnvRegion], env[EnvAccessKey], env[EnvSecretKey] = "eu-west-1", "AKIAEXAMPLE", "topsecret"
	tgt, ok, err := S3TargetFromEnv(get)
	if !ok || err != nil {
		t.Fatalf("complete: ok=%v err=%v", ok, err)
	}
	if tgt.Endpoint != "https://s3.eu-west-1.amazonaws.com" || tgt.Bucket != "my-site" {
		t.Fatalf("target = %+v", tgt)
	}
	env[EnvBucket] = "X"
	if _, _, err := S3TargetFromEnv(get); err == nil || strings.Contains(err.Error(), "topsecret") {
		t.Fatalf("bad bucket err = %v", err)
	}
}

func TestS3TargetFromEnv_PrivateEndpoint(t *testing.T) {
	env := map[string]string{EnvBucket: "my-site", EnvRegion: "eu-west-1", EnvAccessKey: "AKIAEXAMPLE",
		EnvSecretKey: "topsecret", EnvEndpoint: "http://169.254.169.254"}
	get := func(k string) string { return env[k] }
	_, ok, err := S3TargetFromEnv(get)
	if !ok || err == nil || !strings.HasPrefix(err.Error(), "endpoint refused: private or link-local address") {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	env[EnvS3AllowPrivate] = "1" // reaches Validate, which wants https for a link-local address
	if _, _, err := S3TargetFromEnv(get); err == nil || strings.Contains(err.Error(), "endpoint refused") {
		t.Fatalf("opt-in err = %v", err)
	}
	env[EnvEndpoint] = "https://10.0.0.5"
	if _, _, err := S3TargetFromEnv(get); err != nil {
		t.Fatalf("opt-in private https: %v", err)
	}
}
