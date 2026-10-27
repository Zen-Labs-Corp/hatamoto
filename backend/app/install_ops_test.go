package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgsRoutesInstallOps(t *testing.T) {
	for _, args := range [][]string{{"theme", "activate", "t"}, {"apikey", "create", "--name", "n"}, {"plugin", "settings", "set", "p", "a=b"}, {"package", "list"}} {
		if cmd, err := parseArgs(args); err != nil || cmd != cmdPackages {
			t.Errorf("%v: %v %v", args, cmd, err)
		}
	}
	if _, err := parseArgs([]string{"apikey"}); err == nil {
		t.Error("`apikey` alone is not a command")
	}
}

const testSettingsSchema = `{"type":"object","required":["site"],"properties":{
 "site":{"type":"string"},"limit":{"type":"integer"},"on":{"type":"boolean"},
 "tags":{"type":"array"},"token":{"type":"string","writeOnly":true},"free":{}}}`

func TestApplySettingPairs(t *testing.T) {
	old := json.RawMessage(`{"site":"a","token":"secret","keep":1}`)
	got, keys, err := applySettingPairs([]byte(testSettingsSchema), old,
		[]string{"site=123", "limit=5", "on=true", `tags=["x","y"]`, "free=hello", "token="})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	// string stays text, typed values are JSON, an empty write-only value keeps the stored secret.
	if m["site"] != "123" || m["limit"] != float64(5) || m["on"] != true || m["free"] != "hello" || m["token"] != "secret" || m["keep"] != float64(1) {
		t.Fatalf("settings = %s", got)
	}
	if len(m["tags"].([]any)) != 2 || strings.Join(keys, ",") != "site,limit,on,tags,free,token" {
		t.Fatalf("tags/keys = %v %v", m["tags"], keys)
	}
}

func TestApplySettingPairsRefuses(t *testing.T) {
	for pair, want := range map[string]string{
		"nokey":      "not key=value",
		"limit=five": "not an integer",
		"on=yes":     "true or false",
		"tags=[bad":  "valid JSON",
		"unknown=1":  "no setting",
	} {
		if _, _, err := applySettingPairs([]byte(testSettingsSchema), nil, []string{pair}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", pair, err, want)
		}
	}
}

func TestResolveSettingSources(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSettingSources([]string{"a=plain", "token=@" + f, "other=-", "n=x=y"}, strings.NewReader("from-stdin\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a=plain", "token=s3cret", "other=from-stdin", "n=x=y"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	lit, err := resolveSettingSources([]string{"h=@@handle"}, nil)
	if err != nil || lit[0] != "h=@handle" {
		t.Fatalf("@@ escape = %v, %v", lit, err)
	}
	if _, err := resolveSettingSources([]string{"a=-", "b=-"}, strings.NewReader("x")); err == nil {
		t.Error("two stdin values must be refused")
	}
	if _, err := resolveSettingSources([]string{"a=@/no/such/file"}, nil); err == nil {
		t.Error("a missing file must be an error")
	}
}
