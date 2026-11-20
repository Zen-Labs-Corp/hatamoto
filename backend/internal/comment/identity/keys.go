package identity

import "hatamoto.dev/core/internal/secretbox"

// deviceKeyDomain is distinct from the comment model's "hatamoto/comment/..."
// labels, so no key is shared between purposes.
const deviceKeyDomain = "hatamoto/comments/device-cookie"

// DeviceKey is the hm_c MAC key. A changed label signs every visitor out, so
// keys_test.go pins its bytes.
func DeviceKey(serverSecret string) []byte {
	return secretbox.DeriveKey(serverSecret, deviceKeyDomain)
}
