package runenv

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for raw, want := range map[string]Mode{
		"": ModeProduction, " Production ": ModeProduction, "STAGING": ModeStaging, "staging": ModeStaging,
	} {
		got, err := Parse(raw)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"stagin", "dev"} {
		if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), EnvVar) {
			t.Errorf("Parse(%q) err = %v; want error naming %s", raw, err, EnvVar)
		}
	}
}

func TestStagingReadsEnvPerCall(t *testing.T) {
	for val, want := range map[string]bool{"": false, "production": false, "staging": true, "garbage": true} {
		t.Setenv(EnvVar, val)
		if got := Staging(); got != want {
			t.Errorf("Staging() with %q = %v; want %v", val, got, want)
		}
	}
}

func TestSuppress(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	t.Setenv(EnvVar, "production")
	if Suppress(context.Background(), log, Mail) || buf.Len() != 0 {
		t.Fatalf("production: suppressed or logged %q", buf.String())
	}

	t.Setenv(EnvVar, "staging")
	if !Suppress(context.Background(), log, Webhooks, "urls", 3) {
		t.Fatal("staging: not suppressed")
	}
	out := buf.String()
	if !strings.Contains(out, "staging mode: suppressed") || !strings.Contains(out, "webhooks") || !strings.Contains(out, "urls=3") {
		t.Errorf("log line = %q", out)
	}
}

func TestSummary(t *testing.T) {
	want := "staging mode: mail, webhooks, IndexNow, cache purge, off-site backup, updater, plugin outbound HTTP, install statistics, static export to S3, static export over SFTP and media deletions in object storage are off"
	if got := Summary(); got != want {
		t.Errorf("Summary() = %q", got)
	}
	if n := len(Families()); n != 11 {
		t.Errorf("Families() = %d", n)
	}
}
