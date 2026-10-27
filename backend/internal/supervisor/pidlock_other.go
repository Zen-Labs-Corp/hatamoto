//go:build !unix

package supervisor

type pidLock struct{}

func acquirePIDLock(string) (*pidLock, error) { return nil, errUnsupported }
func (*pidLock) release()                     {}
func pidLockHolder(string) (int, bool)        { return 0, false }
