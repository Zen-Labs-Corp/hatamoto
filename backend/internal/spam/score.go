package spam

import (
	"context"
	"strings"
	"time"
	"unicode"
)

// Signal is one scored finding a caller adds (blocklist hit, duplicate body, comment.check).
type Signal struct {
	Reason string
	Points int
}

// Points awarded per rule kind, and the score thresholds for the buckets.
const (
	PointsTooFast   = 10
	PointsWord      = 5
	PointsScript    = 5
	PointsBlocklist = 10
	PointsDuplicate = 5
	HoldScore       = 3  // at or above: Hold
	SpamScore       = 10 // at or above: Spam
)

// Reasons Score reports besides ReasonWord and ReasonScript. Rule names only.
const (
	ReasonTooFast   = "submitted too fast"
	ReasonBlocklist = "blocklist"
	ReasonDuplicate = "duplicate body"
)

// ScoreInput is one comment as Score sees it.
type ScoreInput struct {
	Subject string   // e.g. CommentSubject(entityID)
	Timing  string   // hm_ts token
	Texts   []string // name + body (no control fields)
	Signals []Signal
}

// ScoreResult is the outcome of Score. Verdict is one of Allow, Hold, Spam.
type ScoreResult struct {
	Verdict Verdict
	Score   int
	Reasons []string
}

// Score is the comments' three-bucket check: additive points from timing, blocked
// words and blocked scripts, plus the caller's signals. It ignores Settings.Action
// (Refuse is form-only) and MaxLinks (comments hold on any link elsewhere).
func (p *Pipeline) Score(ctx context.Context, in ScoreInput) ScoreResult {
	set, err := p.Settings.Get(ctx)
	if err != nil {
		set = Defaults()
	}
	now := p.now()
	issued, ok := CheckToken(p.TimingKey, in.Subject, in.Timing, now)
	return ScoreWith(set, in, issued, ok, now)
}

// ScoreWith is the pure core of Score: settings and the timing token's issue
// time are passed in. Each rule kind awards its points once, not per match.
func ScoreWith(set Settings, in ScoreInput, issued time.Time, issuedOK bool, now time.Time) ScoreResult {
	var res ScoreResult
	add := func(reason string, points int) {
		res.Score += points
		res.Reasons = append(res.Reasons, reason)
	}

	if set.MinSeconds > 0 && issuedOK && now.Sub(issued) < time.Duration(set.MinSeconds)*time.Second {
		add(ReasonTooFast, PointsTooFast)
	}

	matchers := wordMatchers(set.BlockedWords)
words:
	for _, t := range in.Texts {
		lower := strings.ToLower(t)
		for _, m := range matchers {
			if m(lower) {
				add(ReasonWord, PointsWord)
				break words
			}
		}
	}

	if len(set.BlockedScripts) > 0 {
		tables := scriptRanges(set.BlockedScripts)
	scripts:
		for _, t := range in.Texts {
			for _, r := range t {
				if unicode.IsOneOf(tables, r) {
					add(ReasonScript, PointsScript)
					break scripts
				}
			}
		}
	}

	for _, s := range in.Signals {
		add(s.Reason, s.Points)
	}

	switch {
	case res.Score >= SpamScore:
		res.Verdict = Spam
	case res.Score >= HoldScore:
		res.Verdict = Hold
	default:
		res.Verdict = Allow
	}
	return res
}
