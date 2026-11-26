package email

import (
	"errors"
	"net/netip"

	"hatamoto.dev/core/internal/safefetch"
)

var (
	// ErrNoneCredentials: a username or password would travel unencrypted.
	ErrNoneCredentials = errors.New("security none sends a username and password only to localhost, 127.0.0.1 or ::1")
	// ErrNonePublicAddr: the address is not loopback or on a private network.
	ErrNonePublicAddr = errors.New("security none only connects to localhost or a private network address")
)

// IsLoopbackHost reports whether host is one of the three names
// smtp.PlainAuth accepts without TLS. It is an exact match.
func IsLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// CheckNone is the one rule for Security None, shared by the admin form,
// saved settings, SMTP_* env and dial. host is lower-cased without a
// trailing dot. Names are judged at connect time; an IP literal is judged
// here.
func CheckNone(host string, hasCredentials bool) error {
	if IsLoopbackHost(host) {
		return nil
	}
	if hasCredentials {
		return ErrNoneCredentials
	}
	if ip, err := netip.ParseAddr(host); err == nil && !safefetch.PrivateNetworkAddr(ip) {
		return ErrNonePublicAddr
	}
	return nil
}

// AddrRefusedError is a connect refused because Addr is neither loopback nor
// on a private network.
type AddrRefusedError struct{ Addr netip.Addr }

func (e *AddrRefusedError) Error() string {
	return "security none refused " + e.Addr.String() + ": not localhost or a private network address"
}

func (e *AddrRefusedError) Unwrap() error { return ErrNonePublicAddr }
