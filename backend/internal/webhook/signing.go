package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// Sign is the X-Webhook-Signature value: "sha256=" and the hex HMAC-SHA256 of
// the exact request body, keyed by the subscription's secret.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// SignV2 is the X-Webhook-Signature-V2 value: "t=<unix>,v1=<hex>" where the
// HMAC-SHA256 covers "<unix>.<body>", so a receiver can reject a request whose
// t is older than its tolerance (five minutes is usual).
func SignV2(secret string, unix int64, body []byte) string {
	t := strconv.FormatInt(unix, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(t + "."))
	mac.Write(body)
	return "t=" + t + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
