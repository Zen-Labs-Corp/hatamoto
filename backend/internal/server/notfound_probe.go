package server

import (
	"log/slog"
	"sync"
	"time"
)

const probeFlushEvery = time.Minute

// probeCounter counts probes of well-known scanner paths by name and logs one
// summary line per name each interval. It records names and counts only: never
// a path, query, address or user agent. The flush goroutine runs only while
// there is something to report and exits after an idle interval, so an idle
// server (and a test) holds no goroutine.
type probeCounter struct {
	mu       sync.Mutex
	counts   map[string]int
	running  bool
	stop     chan struct{}
	logger   *slog.Logger
	interval time.Duration
}

func newProbeCounter(logger *slog.Logger) *probeCounter {
	return &probeCounter{counts: map[string]int{}, logger: logger, interval: probeFlushEvery, stop: make(chan struct{})}
}

// hit counts one probe of the well-known path called name.
func (p *probeCounter) hit(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[name]++
	if !p.running {
		p.running = true
		go p.loop()
	}
}

func (p *probeCounter) loop() {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			if !p.flush() {
				p.mu.Lock()
				// Re-check under the lock: a hit between flush and here keeps us alive.
				if len(p.counts) == 0 {
					p.running = false
					p.mu.Unlock()
					return
				}
				p.mu.Unlock()
			}
		}
	}
}

// flush logs and resets the counts; it reports whether it logged anything.
func (p *probeCounter) flush() bool {
	p.mu.Lock()
	counts := p.counts
	p.counts = map[string]int{}
	p.mu.Unlock()
	for name, n := range counts {
		p.logger.Info("admin probe summary", "event", "admin_probe", "name", name, "count", n)
	}
	return len(counts) > 0
}

// Stop ends the flush goroutine (server shutdown, tests) after a final flush.
func (p *probeCounter) Stop() {
	p.mu.Lock()
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	p.mu.Unlock()
	p.flush()
}
