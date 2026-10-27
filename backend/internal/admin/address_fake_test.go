package admin

import (
	"context"
	"sync"

	"hatamoto.dev/core/internal/adminpath"
)

// fakeAddress is an AddressService for handler tests. Set the exported-style
// fields before the request; read the call logs after it.
type fakeAddress struct {
	mu       sync.Mutex
	state    AddressState
	assess   func(path string) adminpath.Result
	next     string // what Generate returns
	applyErr error
	keepErr  error

	applied []ApplyRequest
	kept    []string
}

func (f *fakeAddress) State() AddressState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeAddress) Assess(_ context.Context, path string) adminpath.Result {
	if f.assess != nil {
		return f.assess(path)
	}
	return adminpath.Result{Path: path, Level: adminpath.LevelStrong}
}

func (f *fakeAddress) Generate() string { return f.next }

func (f *fakeAddress) Apply(_ context.Context, req ApplyRequest) (ApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, req)
	if f.applyErr != nil {
		return ApplyResult{}, f.applyErr
	}
	f.state.Path = req.Path
	return ApplyResult{Path: req.Path}, nil
}

func (f *fakeAddress) Keep(_ context.Context, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kept = append(f.kept, by)
	return f.keepErr
}

var _ AddressService = (*fakeAddress)(nil)
