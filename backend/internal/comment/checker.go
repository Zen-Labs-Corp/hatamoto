package comment

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CheckInput is what a comment.check hook sees: never the raw IP or email.
type CheckInput struct {
	EntityID    uuid.UUID
	Schema      string // entity schema slug
	Body        string // body_raw
	LinkDomains []string
	BodyHash    string
	IPHash      string
	NetHash     string
}

// Checker adds to the spam score (the plugin hook adapter implements it).
type Checker interface {
	Check(ctx context.Context, in CheckInput) (score int, reasons []string, err error)
}

// DefaultCheckTimeout bounds all checkers together.
const DefaultCheckTimeout = 500 * time.Millisecond

// Reasons RunCheckers adds when it holds a comment.
const (
	ReasonCheckFailed   = "check failed"
	ReasonCheckTimedOut = "check timed out"
)

type checkResult struct {
	score   int
	reasons []string
	err     error
}

// RunCheckers sums the scores of all checkers. On any error, panic or
// deadline it returns hold = true with a reason and keeps the partial score. A
// checker that ignores its context cannot block past the timeout.
func RunCheckers(ctx context.Context, cs []Checker, in CheckInput, timeout time.Duration) (score int, reasons []string, hold bool) {
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, c := range cs {
		done := make(chan checkResult, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- checkResult{err: fmt.Errorf("checker panic: %v", r)}
				}
			}()
			s, rs, err := c.Check(ctx, in)
			done <- checkResult{s, rs, err}
		}()
		select {
		case r := <-done:
			score += r.score
			reasons = append(reasons, r.reasons...)
			if r.err != nil {
				return score, append(reasons, ReasonCheckFailed), true
			}
		case <-ctx.Done():
			return score, append(reasons, ReasonCheckTimedOut), true
		}
	}
	return score, reasons, false
}
