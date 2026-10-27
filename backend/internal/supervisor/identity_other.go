//go:build !unix

package supervisor

const noFollow = 0

// CheckOwner is a no-op off unix; the supervisor does not run there.
func CheckOwner(Layout) error { return nil }
