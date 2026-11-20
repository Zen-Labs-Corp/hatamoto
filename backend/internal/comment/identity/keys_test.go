package identity

import (
	"bytes"
	"encoding/hex"
	"testing"

	"hatamoto.dev/core/internal/secretbox"
)

const testSecret = "identity test server secret value"

func TestDeviceKeyIsPinned(t *testing.T) {
	t.Parallel()
	const want = "92d252e8d7b52a7c05bbb90f542450b2419556e2904a27d38931460333823d22"
	if got := hex.EncodeToString(DeviceKey(testSecret)); got != want {
		t.Errorf("DeviceKey = %s, want %s (changing it signs every commenter out)", got, want)
	}
	for _, label := range []string{"email-seal", "email-hash", "ip-hash", "net-hash"} {
		if bytes.Equal(DeviceKey(testSecret), secretbox.DeriveKey(testSecret, "hatamoto/comment/"+label)) {
			t.Errorf("device key equals the model's %s key", label)
		}
	}
}
