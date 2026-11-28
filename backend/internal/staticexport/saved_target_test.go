package staticexport

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/s3/s3test"
	"hatamoto.dev/core/internal/safefetch"
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

func guardTarget(endpoint string) offsite.Target {
	return offsite.Target{Endpoint: endpoint, Region: s3test.Region, Bucket: s3test.Bucket,
		AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}
}

func TestS3TargetChecked_Literal(t *testing.T) {
	none := func(string) string { return "" }
	optIn := func(k string) string {
		if k == EnvS3AllowPrivate {
			return "1"
		}
		return ""
	}
	for _, ep := range []string{"https://169.254.169.254", "http://127.0.0.1:9000"} {
		if _, err := S3TargetChecked(guardTarget(ep), none); !errors.Is(err, ErrPrivateEndpoint) {
			t.Errorf("%s: err = %v, want refusal", ep, err)
		}
		if _, err := S3TargetChecked(guardTarget(ep), optIn); err != nil {
			t.Errorf("%s with opt-in: %v", ep, err)
		}
	}
}

func TestS3TargetChecked_DialGuard(t *testing.T) {
	lookup := safefetch.Lookup(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.7")}, nil
	})
	tgt, err := s3TargetChecked(guardTarget("https://s3.internal.test"), func(string) string { return "" }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tgt.(*S3Target).Previous(context.Background()); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("Previous err = %v, want ErrBlocked", err)
	}
}

func TestS3TargetChecked_AllowPrivateReachesLoopback(t *testing.T) {
	srv := s3test.NewServer(t)
	optIn := func(k string) string {
		if k == EnvS3AllowPrivate {
			return "1"
		}
		return ""
	}
	tgt, err := S3TargetChecked(guardTarget(srv.URL), optIn)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := tgt.(*S3Target).Previous(context.Background()); err != nil || found {
		t.Fatalf("Previous: found=%v err=%v", found, err)
	}
}

func TestS3TargetChecked_DialRefusalNamesOptIn(t *testing.T) {
	lookup := safefetch.Lookup(func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.7")}, nil
	})
	tgt, err := s3TargetChecked(guardTarget("https://s3.internal.test"), func(string) string { return "" }, lookup)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = tgt.(*S3Target).Previous(context.Background())
	if err == nil || !strings.Contains(err.Error(), "endpoint refused: private or link-local address") || !strings.Contains(err.Error(), EnvS3AllowPrivate) {
		t.Fatalf("err = %v, want the refusal naming %s", err, EnvS3AllowPrivate)
	}
}

func sealedSFTP(t *testing.T, key []byte, plain string) Settings {
	t.Helper()
	sealed, err := SealSFTPSecret(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	set := validSFTP()
	set.SFTPSecretSealed, set.SFTPHostKey, set.SFTPHTAccess = sealed, "SHA256:abc", true
	return set
}

func TestSavedTarget_SFTP(t *testing.T) {
	const plain = "sftp-password-123"
	key := SecretKey("test-secret")
	set := sealedSFTP(t, key, plain)
	none := func(string) string { return "" }
	tg, err := SavedTarget(set, key, DirGuard{}, none)
	if err != nil {
		t.Fatal(err)
	}
	if want := SFTPKey("site", "ftp.example.com", 22, "public_html"); tg.Key() != want {
		t.Fatalf("key = %q, want %q", tg.Key(), want)
	}
	if strings.Contains(tg.Key()+tg.Describe(), plain) {
		t.Fatal("the secret is in the key or description")
	}
	h, ok := tg.(interface{ WritesHTAccess() bool })
	if !ok || !h.WritesHTAccess() {
		t.Fatal("the .htaccess switch is not carried over")
	}
	set.SFTPHTAccess = false
	tg, _ = SavedTarget(set, key, DirGuard{}, none)
	if tg.(interface{ WritesHTAccess() bool }).WritesHTAccess() {
		t.Fatal("the .htaccess switch off is ignored")
	}
}

func TestSavedTarget_SFTPPrivateLiteralRefused(t *testing.T) {
	key := SecretKey("test-secret")
	optIn := func(k string) string {
		if k == EnvSFTPAllowPrivate {
			return "1"
		}
		return ""
	}
	s3Opt := func(k string) string {
		if k == EnvS3AllowPrivate {
			return "1"
		}
		return ""
	}
	for _, host := range []string{"127.0.0.1", "localhost", "10.1.2.3"} {
		set := sealedSFTP(t, key, "pw")
		set.SFTPHost = host
		_, err := SavedTarget(set, key, DirGuard{}, func(string) string { return "" })
		if !errors.Is(err, ErrPrivateSFTPHost) || !strings.Contains(err.Error(), EnvSFTPAllowPrivate) {
			t.Errorf("%s: err = %v", host, err)
		}
		if _, err := SavedTarget(set, key, DirGuard{}, s3Opt); !errors.Is(err, ErrPrivateSFTPHost) {
			t.Errorf("%s: the S3 opt-in opened SFTP: %v", host, err)
		}
		if tg, err := SavedTarget(set, key, DirGuard{}, optIn); err != nil || tg == nil {
			t.Errorf("%s with opt-in: %v", host, err)
		}
	}
}

func TestSavedTarget_SFTPSecretLost(t *testing.T) {
	const plain = "sftp-password-123"
	set := sealedSFTP(t, SecretKey("test-secret"), plain)
	_, err := SavedTarget(set, SecretKey("another-secret"), DirGuard{}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be opened") || strings.Contains(err.Error(), plain) {
		t.Fatalf("err = %v", err)
	}
}

func TestSavedTarget_SFTPDoesNotUseS3Secret(t *testing.T) {
	key := SecretKey("test-secret")
	s3Blob, err := SealSecret(key, "s3-secret")
	if err != nil {
		t.Fatal(err)
	}
	set := validSFTP()
	set.SFTPSecretSealed = nil
	set.SecretSealed = s3Blob
	_, err = SavedTarget(set, key, DirGuard{}, nil)
	if err == nil || !strings.Contains(err.Error(), "is not saved") {
		t.Fatalf("err = %v", err)
	}
	set.SFTPSecretSealed = s3Blob // an S3 blob in the SFTP column must not open either
	if _, err = SavedTarget(set, key, DirGuard{}, nil); err == nil || !strings.Contains(err.Error(), "cannot be opened") {
		t.Fatalf("err = %v", err)
	}
}
