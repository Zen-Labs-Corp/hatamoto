package email

import (
	"errors"
	"testing"
)

func TestCheckNone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host      string
		noCreds   error
		withCreds error
	}{
		{"localhost", nil, nil},
		{"127.0.0.1", nil, nil},
		{"::1", nil, nil},
		{"mailpit", nil, ErrNoneCredentials},
		{"10.0.0.5", nil, ErrNoneCredentials},
		{"100.64.1.1", nil, ErrNoneCredentials},
		{"fd12::1", nil, ErrNoneCredentials},
		{"203.0.113.5", ErrNonePublicAddr, ErrNoneCredentials},
		{"smtp.example.test", nil, ErrNoneCredentials},
		{"0.0.0.0", ErrNonePublicAddr, ErrNoneCredentials},
	}
	for _, tc := range tests {
		if err := CheckNone(tc.host, false); !errors.Is(err, tc.noCreds) || (tc.noCreds == nil && err != nil) {
			t.Errorf("CheckNone(%q, false) = %v, want %v", tc.host, err, tc.noCreds)
		}
		if err := CheckNone(tc.host, true); !errors.Is(err, tc.withCreds) || (tc.withCreds == nil && err != nil) {
			t.Errorf("CheckNone(%q, true) = %v, want %v", tc.host, err, tc.withCreds)
		}
	}
}
