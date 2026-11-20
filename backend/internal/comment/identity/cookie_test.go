package identity

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	cookieNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	cookieID  = uuid.MustParse("55555555-5555-5555-5555-555555555555")
)

func signedAt(key []byte, version int) string {
	return SignDevice(key, DeviceClaims{CommenterID: cookieID, TokenVersion: version, Expires: cookieNow.Add(time.Hour)})
}

func TestDeviceCookie_RoundTrip(t *testing.T) {
	t.Parallel()
	key := DeviceKey(testSecret)
	for _, version := range []int{1, 7} {
		c, ok := VerifyDevice(key, signedAt(key, version), cookieNow)
		if !ok || c.CommenterID != cookieID || c.TokenVersion != version || !c.Expires.Equal(cookieNow.Add(time.Hour)) {
			t.Errorf("version %d: got %+v ok=%v", version, c, ok)
		}
	}
}

func TestDeviceCookie_ForgedRejected(t *testing.T) {
	t.Parallel()
	key := DeviceKey(testSecret)
	good := signedAt(key, 1)
	parts := strings.Split(good, ".")
	if len(parts) != 5 {
		t.Fatalf("cookie has %d parts: %q", len(parts), good)
	}
	swap := func(i int, v string) string {
		p := append([]string(nil), parts...)
		p[i] = v
		return strings.Join(p, ".")
	}
	flipped := good[:len(good)-1] + map[bool]string{true: "B", false: "A"}[good[len(good)-1] == 'A']
	signedWith := func(payload string) string { // a validly MACed payload that fails parsing
		return payload + "." + cookieMAC(key, payload)
	}
	exp := strconv.FormatInt(cookieNow.Add(time.Hour).Unix(), 10)
	cases := map[string]string{
		"flipped mac":        flipped,
		"other key":          signedAt(DeviceKey("another test server secret value"), 1),
		"edited commenter":   swap(2, "66666666-6666-6666-6666-666666666666"),
		"edited version":     swap(3, "2"),
		"v2 prefix":          swap(0, "v2"),
		"missing part":       strings.Join(append(append([]string(nil), parts[:3]...), parts[4]), "."),
		"empty":              "",
		"oversized":          good + strings.Repeat("a", maxCookieLen),
		"not a uuid":         signedWith("v1." + exp + ".nope.1"),
		"version zero":       signedWith("v1." + exp + "." + cookieID.String() + ".0"),
		"negative version":   signedWith("v1." + exp + "." + cookieID.String() + ".-1"),
		"non numeric ver":    signedWith("v1." + exp + "." + cookieID.String() + ".x"),
		"non numeric exp":    signedWith("v1.x." + cookieID.String() + ".1"),
		"extra payload part": signedWith("v1." + exp + "." + cookieID.String() + ".1.1"),
	}
	for name, val := range cases {
		if _, ok := VerifyDevice(key, val, cookieNow); ok {
			t.Errorf("%s: accepted %q", name, val)
		}
	}
}

func TestDeviceCookie_Expired(t *testing.T) {
	t.Parallel()
	key := DeviceKey(testSecret)
	val := SignDevice(key, DeviceClaims{CommenterID: cookieID, TokenVersion: 1, Expires: cookieNow})
	if _, ok := VerifyDevice(key, val, cookieNow); ok {
		t.Error("accepted at the expiry second")
	}
	if _, ok := VerifyDevice(key, val, cookieNow.Add(-time.Second)); !ok {
		t.Error("rejected one second before expiry")
	}
}
