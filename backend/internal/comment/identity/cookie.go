package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CookieName is the device cookie.
const CookieName = "hm_c"

const maxCookieLen = 256

// DeviceClaims is what the hm_c cookie asserts.
type DeviceClaims struct {
	CommenterID  uuid.UUID
	TokenVersion int // hatamoto_commenters.token_version; the column defaults to 1
	Expires      time.Time
}

func cookieMAC(key []byte, payload string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("c|" + payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SignDevice returns v1.<expiryUnix>.<commenterID>.<tokenVersion>.<mac>.
func SignDevice(key []byte, c DeviceClaims) string {
	payload := "v1." + strconv.FormatInt(c.Expires.Unix(), 10) + "." + c.CommenterID.String() + "." + strconv.Itoa(c.TokenVersion)
	return payload + "." + cookieMAC(key, payload)
}

// VerifyDevice checks the format, the MAC and the expiry. It does not check
// the stored token_version; Service.Device does.
func VerifyDevice(key []byte, val string, now time.Time) (DeviceClaims, bool) {
	if len(val) > maxCookieLen {
		return DeviceClaims{}, false
	}
	i := strings.LastIndexByte(val, '.')
	if i < 0 {
		return DeviceClaims{}, false
	}
	payload, sig := val[:i], val[i+1:]
	if !hmac.Equal([]byte(sig), []byte(cookieMAC(key, payload))) {
		return DeviceClaims{}, false
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return DeviceClaims{}, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() >= exp {
		return DeviceClaims{}, false
	}
	id, err := uuid.Parse(parts[2])
	if err != nil {
		return DeviceClaims{}, false
	}
	ver, err := strconv.Atoi(parts[3])
	if err != nil || ver < 1 {
		return DeviceClaims{}, false
	}
	return DeviceClaims{CommenterID: id, TokenVersion: ver, Expires: time.Unix(exp, 0).UTC()}, true
}
