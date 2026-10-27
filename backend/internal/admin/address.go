package admin

import (
	"context"
	"errors"
	"time"

	"hatamoto.dev/core/internal/adminpath"
)

// AddressLock says why the admin address cannot be changed from the admin.
type AddressLock string

const (
	// AddressUnlocked: an owner may change the address.
	AddressUnlocked AddressLock = ""
	// AddressLockedEnv: the server environment pins the address.
	AddressLockedEnv AddressLock = "env"
	// AddressLockedDemo: the public demo does not allow changes.
	AddressLockedDemo AddressLock = "demo"
)

// AddressState is what the admin shows about the live admin address. The
// service keeps it cached, so reading it costs no database round trip.
type AddressState struct {
	Path    string
	Level   adminpath.Level
	Reasons []adminpath.Reason
	// SetBy and SetAt are nil for an address the server generated itself and
	// for installs that predate the record.
	SetBy string
	SetAt *time.Time
	// AckBy and AckAt are set when an owner knowingly accepted a weak address.
	AckBy string
	AckAt *time.Time
	Lock  AddressLock
	// Pinned is the environment variable name behind AddressLockedEnv. It is
	// data from the server so no template spells the address or its source.
	Pinned string
}

// ApplyRequest asks for a new admin address. Path is already normalised.
type ApplyRequest struct {
	Path string
	// By is the display name or email of the owner choosing it.
	By string
	// Ack is true when the owner typed the confirmation for a weak address.
	Ack bool
}

// ApplyResult is what a successful Apply reports; Path is the live address.
type ApplyResult struct{ Path string }

// ErrAddressRefused means the address was reserved, invalid, or weak without
// an acknowledgement. Nothing was stored and the old address keeps serving.
var ErrAddressRefused = errors.New("admin address refused")

// AddressService is the admin's view of the live admin address. The server
// implements it: changing the address rebuilds the admin and swaps it in.
type AddressService interface {
	State() AddressState
	// Assess ranks a candidate against the reserved names and the site.
	Assess(ctx context.Context, path string) adminpath.Result
	// Generate returns a new random strong candidate. It stores nothing.
	Generate() string
	// Apply stores req.Path and serves the admin there from now on.
	Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error)
	// Keep records that an owner knowingly accepted the current address.
	Keep(ctx context.Context, by string) error
}
