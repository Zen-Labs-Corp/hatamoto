package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ISC-A-2: the loopback client cannot reach anything else.
func TestLoopbackClientRefusesNonLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, url := range []string{"http://192.0.2.1:80/", "http://[2001:db8::1]:80/"} {
		t0 := time.Now()
		if _, err := get(ctx, loopbackClient(), url, ""); err == nil || !strings.Contains(err.Error(), "non-loopback") {
			t.Errorf("%s: err = %v, want non-loopback refusal", url, err)
		}
		if time.Since(t0) > 2*time.Second {
			t.Errorf("%s: refusal was not immediate", url)
		}
	}
}

// The package may only use its own loopback client and imports no database
// code of its own.
func TestPackageHasNoOtherNetworkOrDB(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	banned := []string{"http.Get(", "http.Post(", "http.PostForm(", "http.Head(", "http.DefaultClient", "http.DefaultTransport",
		`"database/sql"`, "jackc/pgx", "internal/database"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range banned {
			if strings.Contains(string(b), s) {
				t.Errorf("%s contains %q", f, s)
			}
		}
	}
}
