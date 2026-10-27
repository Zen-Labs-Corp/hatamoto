package agents

import "sync"

type fakeRecorder struct {
	mu   sync.Mutex
	hits []RawHit
	// With capped set, Record refuses every hit once capacity were accepted.
	capped   bool
	capacity int
}

func (f *fakeRecorder) Record(h RawHit) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.capped && len(f.hits) >= f.capacity {
		return false
	}
	f.hits = append(f.hits, h)
	return true
}
