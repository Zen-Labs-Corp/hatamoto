package pluginhost

import (
	"bytes"
	"encoding/json/jsontext"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sealSchema = `{"type":"object","properties":{
	"api_key":{"type":"string","writeOnly":true},
	"limit":{"type":"integer","writeOnly":true},
	"cfg":{"type":"object","writeOnly":true},
	"region":{"type":"string"}}}`

func testKey(s string) []byte { return SettingsKey(s + strings.Repeat("-", 32)) }

func TestSealWriteOnlyRoundTrip(t *testing.T) {
	k := testKey("a")
	for name, in := range map[string]string{
		"string": `{"api_key":"k-123"}`,
		"number": `{"limit":42}`,
		"object": `{"cfg":{"token":"t-1","n":[1,2]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			sealed, err := sealWriteOnly(k, "paid-lookup", []byte(sealSchema), jsontext.Value(in))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(sealed), SealedPrefix) {
				t.Fatalf("value not sealed: %s", sealed)
			}
			for _, plain := range []string{`"k-123"`, `"t-1"`, `"limit":42`} {
				if strings.Contains(string(sealed), plain) {
					t.Fatalf("plain value %q left in sealed output", plain)
				}
			}
			opened, bad := openSealed(k, "paid-lookup", sealed)
			if len(bad) != 0 {
				t.Fatalf("unreadable: %v", bad)
			}
			if !reflect.DeepEqual(parseObject(opened), parseObject(jsontext.Value(in))) {
				t.Fatalf("round trip = %s, want %s", opened, in)
			}
		})
	}
}

func TestSealWriteOnlyNoDoubleSeal(t *testing.T) {
	k := testKey("a")
	once, err := sealWriteOnly(k, "p", []byte(sealSchema), jsontext.Value(`{"api_key":"k-123"}`))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := sealWriteOnly(k, "p", []byte(sealSchema), once)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("sealed value changed on second seal:\n%s\n%s", once, twice)
	}
}

func TestSealWriteOnlyLeavesOthers(t *testing.T) {
	k := testKey("a")
	in := jsontext.Value(`{"region":"eu","api_key":"","limit":null}`)
	out, err := sealWriteOnly(k, "p", []byte(sealSchema), in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("nothing to seal, got %s", out)
	}
	out, err = sealWriteOnly(k, "p", []byte(sealSchema), jsontext.Value(`{"region":"eu","api_key":"k-123"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := parseObject(out)["region"]; string(got) != `"eu"` {
		t.Fatalf("region = %s, want untouched", got)
	}
}

func TestSealWriteOnlyNoKeyFailsClosed(t *testing.T) {
	if _, err := sealWriteOnly(nil, "p", []byte(sealSchema), jsontext.Value(`{"api_key":"k-123"}`)); err == nil {
		t.Fatal("sealing without a key must fail")
	}
	in := jsontext.Value(`{"region":"eu"}`)
	out, err := sealWriteOnly(nil, "p", []byte(sealSchema), in)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("nothing to seal without a key = %s, %v", out, err)
	}
}

func TestSealWriteOnlyPrefixLookalikeRoundTrips(t *testing.T) {
	k := testKey("a")
	in := jsontext.Value(`{"region":"` + SealedPrefix + `not-a-blob"}`)
	sealed, err := sealWriteOnly(k, "p", []byte(sealSchema), in)
	if err != nil {
		t.Fatal(err)
	}
	opened, bad := openSealed(k, "p", sealed)
	if len(bad) != 0 || !reflect.DeepEqual(parseObject(opened), parseObject(in)) {
		t.Fatalf("lookalike = %s (unreadable %v), want %s", opened, bad, in)
	}
}

func TestSealAADBindsPluginAndKey(t *testing.T) {
	k := testKey("a")
	blob, err := sealValue(k, "p1", "api_key", jsontext.Value(`"k-123"`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := openValue(k, "p1", "api_key", blob); !ok {
		t.Fatal("same plugin and key must open")
	}
	if _, sealed, ok := openValue(k, "p1", "other", blob); !sealed || ok {
		t.Fatal("blob moved to another key must not open")
	}
	if _, sealed, ok := openValue(k, "p2", "api_key", blob); !sealed || ok {
		t.Fatal("blob moved to another plugin must not open")
	}
}

func TestOpenSealedUnreadable(t *testing.T) {
	sealed, err := sealWriteOnly(testKey("a"), "p", []byte(sealSchema), jsontext.Value(`{"api_key":"k-123","region":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, k := range map[string][]byte{"other key": testKey("b"), "no key": nil} {
		t.Run(name, func(t *testing.T) {
			out, bad := openSealed(k, "p", sealed)
			if !reflect.DeepEqual(bad, []string{"api_key"}) {
				t.Fatalf("unreadable = %v", bad)
			}
			obj := parseObject(out)
			if _, ok := obj["api_key"]; ok || string(obj["region"]) != `"eu"` {
				t.Fatalf("out = %s, want api_key omitted and region kept", out)
			}
		})
	}
	plain := jsontext.Value(`{"region":"eu"}`)
	if out, bad := openSealed(testKey("a"), "p", plain); !bytes.Equal(out, plain) || bad != nil {
		t.Fatalf("no sealed value must return input unchanged, got %s %v", out, bad)
	}
}

func TestSealLegacy(t *testing.T) {
	k := testKey("a")
	legacy := jsontext.Value(`{"region":"eu","api_key":"k-123","limit":7,"cfg":""}`)
	out, sealed, bad, err := sealLegacy(k, "p", []byte(sealSchema), legacy)
	if err != nil || sealed != 2 || bad != 0 {
		t.Fatalf("sealLegacy = %d sealed, %d unreadable, %v", sealed, bad, err)
	}
	obj := parseObject(out)
	if string(obj["region"]) != `"eu"` || string(obj["cfg"]) != `""` {
		t.Fatalf("non-secret or blank values changed: %s", out)
	}
	again, sealed, bad, err := sealLegacy(k, "p", []byte(sealSchema), out)
	if err != nil || sealed != 0 || bad != 0 || !bytes.Equal(again, out) {
		t.Fatalf("second run = %d sealed, %d unreadable, %v, changed=%v", sealed, bad, err, !bytes.Equal(again, out))
	}
	_, sealed, bad, err = sealLegacy(testKey("b"), "p", []byte(sealSchema), out)
	if err != nil || sealed != 0 || bad != 2 {
		t.Fatalf("other key = %d sealed, %d unreadable, %v", sealed, bad, err)
	}
	if _, _, _, err := sealLegacy(nil, "p", []byte(sealSchema), legacy); err == nil {
		t.Fatal("no key must fail")
	}
}

func TestUnreadableWarnRateLimited(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	now := time.Unix(1_700_000_000, 0)
	w := &unreadableWarner{window: time.Hour, now: func() time.Time { return now }}

	w.warn(log, "paid-lookup", []string{"api_key"})
	w.warn(log, "paid-lookup", []string{"api_key"})
	if n := strings.Count(buf.String(), "level=WARN"); n != 1 {
		t.Fatalf("%d WARN lines in one window, want 1", n)
	}
	line := buf.String()
	if !strings.Contains(line, "plugin=paid-lookup") || !strings.Contains(line, "key=api_key") || strings.Contains(line, "hm-sealed:") {
		t.Fatalf("log line = %q", line)
	}
	w.warn(log, "paid-lookup", []string{"other"})
	if n := strings.Count(buf.String(), "level=WARN"); n != 2 {
		t.Fatalf("a second key must warn once, got %d lines", n)
	}
	now = now.Add(time.Hour + time.Second)
	w.warn(log, "paid-lookup", []string{"api_key"})
	if n := strings.Count(buf.String(), "level=WARN"); n != 3 {
		t.Fatalf("after the window want a new line, got %d", n)
	}
}
