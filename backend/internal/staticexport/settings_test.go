package staticexport

import (
	"strings"
	"testing"
)

func validS3() Settings {
	return Settings{Mode: ModeStatic, Target: TargetS3, Service: "minio", Endpoint: "https://s3.example.com",
		Region: "us-east-1", Bucket: "my-site", AccessKey: "AKIAEXAMPLE", SecretSealed: []byte("sealed")}
}

func validSFTP() Settings {
	return Settings{Mode: ModeStatic, Target: TargetSFTP, SFTPHost: "ftp.example.com", SFTPPort: 22,
		SFTPUser: "site", SFTPAuth: SFTPAuthPassword, SFTPSecretSealed: []byte("sealed"), SFTPDir: "public_html"}
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
		{"sftp valid", func(s *Settings) { *s = validSFTP() }, ""},
		{"sftp no host", func(s *Settings) { *s = validSFTP(); s.SFTPHost = "" }, "SFTP host"},
		{"sftp host with path", func(s *Settings) { *s = validSFTP(); s.SFTPHost = "h.example.com/x" }, "SFTP host"},
		{"sftp port 0", func(s *Settings) { *s = validSFTP(); s.SFTPPort = 0 }, "SFTP port"},
		{"sftp port 70000", func(s *Settings) { *s = validSFTP(); s.SFTPPort = 70000 }, "SFTP port"},
		{"sftp no user", func(s *Settings) { *s = validSFTP(); s.SFTPUser = " " }, "SFTP user"},
		{"sftp bad auth", func(s *Settings) { *s = validSFTP(); s.SFTPAuth = "token" }, "SFTP sign-in"},
		{"sftp key auth ok", func(s *Settings) { *s = validSFTP(); s.SFTPAuth = SFTPAuthKey }, ""},
		{"sftp no secret", func(s *Settings) { *s = validSFTP(); s.SFTPSecretSealed = nil }, "password or private key"},
		{"sftp empty dir", func(s *Settings) { *s = validSFTP(); s.SFTPDir = "" }, "remote folder"},
		{"sftp root dir", func(s *Settings) { *s = validSFTP(); s.SFTPDir = "/" }, "remote folder"},
		{"sftp absolute dir", func(s *Settings) { *s = validSFTP(); s.SFTPDir = "/abs" }, "remote folder"},
		{"sftp home dir", func(s *Settings) { *s = validSFTP(); s.SFTPDir = "~/x" }, "remote folder"},
		{"sftp dot dir", func(s *Settings) { *s = validSFTP(); s.SFTPDir = "." }, "remote folder"},
		{"sftp dotdot dir", func(s *Settings) { *s = validSFTP(); s.SFTPDir = "a/../b" }, "remote folder"},
		{"sftp trailing slash ok", func(s *Settings) { *s = validSFTP(); s.SFTPDir = "public_html/" }, ""},
		{"sftp needs no pinned key", func(s *Settings) { *s = validSFTP(); s.SFTPHostKey = "" }, ""},
		{"served ignores sftp fields", func(s *Settings) { *s = validSFTP(); s.Mode = ModeServed; s.SFTPHost = "" }, ""},
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

func TestValidateRemoteDir_Cleans(t *testing.T) {
	for in, want := range map[string]string{"public_html/": "public_html", "a//b/./c": "a/b/c", " www ": "www"} {
		if got, err := ValidateRemoteDir(in); err != nil || got != want {
			t.Errorf("ValidateRemoteDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestSealOpenSFTPSecret(t *testing.T) {
	key := SecretKey("server-secret")
	sealed, err := SealSFTPSecret(key, "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	s := Settings{SFTPSecretSealed: sealed}
	if got, ok := s.SFTPSecret(key); !ok || got != "hunter2" {
		t.Fatalf("SFTPSecret = %q, %v", got, ok)
	}
	if _, ok := s.SFTPSecret(SecretKey("other-secret")); ok {
		t.Fatal("a wrong server secret opened the SFTP secret")
	}
}

func TestSFTPSecretDoesNotOpenS3Blob(t *testing.T) {
	key := SecretKey("server-secret")
	s3Blob, _ := SealSecret(key, "s3-secret")
	sftpBlob, _ := SealSFTPSecret(key, "sftp-secret")
	if _, ok := (Settings{SFTPSecretSealed: s3Blob}).SFTPSecret(key); ok {
		t.Fatal("an S3 blob opened as the SFTP secret")
	}
	if _, ok := (Settings{SecretSealed: sftpBlob}).Secret(key); ok {
		t.Fatal("an SFTP blob opened as the S3 secret")
	}
}

func TestSettingsDefaultsSFTP(t *testing.T) {
	d := Defaults()
	if d.SFTPPort != 22 || d.SFTPAuth != SFTPAuthPassword || d.SFTPDir != "public_html" || !d.SFTPHTAccess {
		t.Fatalf("defaults = %+v", d)
	}
}
