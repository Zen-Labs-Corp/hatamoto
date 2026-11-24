package app

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/kit"
	"hatamoto.dev/core/internal/packages"
)

const kitFixtureDir = "../internal/kit/testdata/community"

// kitKeyFile writes a throwaway signing key and returns its path.
func kitKeyFile(t *testing.T) string {
	t.Helper()
	k, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := jsonx.Indent(k)
	f := filepath.Join(t.TempDir(), "k.key")
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func runKitCmd(args ...string) (string, error) {
	var out bytes.Buffer
	err := runKit(context.Background(), args, slog.New(slog.DiscardHandler), &out)
	return out.String(), err
}

func TestKitPackThenCheck(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	file := filepath.Join(t.TempDir(), "community.kit.hmp")
	out, err := runKitCmd("pack", kitFixtureDir, "--key", kitKeyFile(t), "--out", file, "--expect-name", "community")
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !strings.Contains(out, "signed by") {
		t.Errorf("pack output = %q", out)
	}
	for _, args := range [][]string{{"check", file}, {"check", "--package", file, "--expect-name", "community"}, {"check", kitFixtureDir}} {
		out, err := runKitCmd(args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "kit community 1.0.0: ok") {
			t.Errorf("%v: output = %q", args, out)
		}
	}
}

func TestKitCheckRefusals(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "kit.json"), []byte(`{"name":"x","version":"1.0.0","kit":1,"sdk":"^0.27","mystery":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runKitCmd("check", bad); err == nil {
		t.Error("a kit.json with an unknown key passed")
	}
	if _, err := runKitCmd("check", kitFixtureDir, "--expect-name", "other"); err == nil || !strings.Contains(err.Error(), "expected") {
		t.Errorf("expect-name mismatch: err = %v", err)
	}
	if _, err := runKitCmd("pack", kitFixtureDir); err == nil {
		t.Error("pack without a key passed")
	}
}

func TestKitUnknownVerbShowsUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}} {
		_, err := runKitCmd(args...)
		if err == nil || !strings.Contains(err.Error(), "kit check") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestKitPackageInstallPointsToApply(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	file := filepath.Join(t.TempDir(), "c.hmp")
	if _, err := runKitCmd("pack", kitFixtureDir, "--key", kitKeyFile(t), "--out", file); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runPackages(context.Background(), []string{"package", "install", file}, slog.New(slog.DiscardHandler), &out)
	if err == nil || !strings.Contains(err.Error(), "kit apply") {
		t.Errorf("err = %v, want a pointer to kit apply", err)
	}
}

func TestPickExtraScopesGrantToAPlugin(t *testing.T) {
	step := func(name string, extra ...string) kit.PackageStep {
		return kit.PackageStep{Kind: packages.KindPlugin, Name: name, Extra: extra}
	}
	pl := &kit.Plan{Plugins: []kit.PackageStep{step("plug-a", "site.scripts", "ui.allow:camera"), step("plug-b", "site.scripts")}}
	var out bytes.Buffer
	got := pickExtra(pl, []string{"plug-b:site.scripts", "ui.allow:camera", "site.scripts", "plug-a:nope", "db"}, &out)
	if want := []string{"plug-b:site.scripts", "plug-a:ui.allow:camera"}; !slices.Equal(got, want) {
		t.Errorf("picked %v, want %v", got, want)
	}
	for _, w := range []string{"--grant site.scripts ignored: plug-a, plug-b all ask", "--grant plug-a:nope ignored", "--grant db ignored"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q: %s", w, out.String())
		}
	}
}
