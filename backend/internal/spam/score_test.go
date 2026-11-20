package spam

import (
	"context"
	"testing"
	"time"
)

func scoreSettings() Settings {
	s := Defaults()
	s.MinSeconds = 3
	s.BlockedWords = []string{"viagra"}
	s.BlockedScripts = []string{"cyrillic"}
	return s
}

func TestScoreBuckets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    ScoreInput
		want  Verdict
		score int
	}{
		{"clean", ScoreInput{Texts: []string{"Ann", "Nice post"}}, Allow, 0},
		{"blocked word", ScoreInput{Texts: []string{"buy viagra now"}}, Hold, PointsWord},
		{"blocklist signal", ScoreInput{Texts: []string{"hi"}, Signals: []Signal{{ReasonBlocklist, PointsBlocklist}}}, Spam, PointsBlocklist},
		{"word and script", ScoreInput{Texts: []string{"viagra", "привет"}}, Spam, PointsWord + PointsScript},
		{"word once per kind", ScoreInput{Texts: []string{"viagra", "viagra viagra"}}, Hold, PointsWord},
		{"links not scored", ScoreInput{Texts: []string{"http://a.example.com http://b.example.com http://c.example.com"}}, Allow, 0},
		{"duplicate signal holds", ScoreInput{Texts: []string{"hi"}, Signals: []Signal{{ReasonDuplicate, PointsDuplicate}}}, Hold, PointsDuplicate},
	}
	for _, c := range cases {
		got := ScoreWith(scoreSettings(), c.in, time.Time{}, false, t0)
		if got.Verdict != c.want || got.Score != c.score {
			t.Errorf("%s: got %v/%d want %v/%d", c.name, got.Verdict, got.Score, c.want, c.score)
		}
	}
}

func TestScoreTiming(t *testing.T) {
	t.Parallel()
	in := ScoreInput{Texts: []string{"hi"}}
	fast := ScoreWith(scoreSettings(), in, t0, true, t0.Add(time.Second))
	if fast.Verdict != Spam || len(fast.Reasons) != 1 || fast.Reasons[0] != ReasonTooFast {
		t.Errorf("too fast: %+v", fast)
	}
	slow := ScoreWith(scoreSettings(), in, t0, true, t0.Add(5*time.Second))
	if slow.Verdict != Allow {
		t.Errorf("slow: %+v", slow)
	}
	none := ScoreWith(scoreSettings(), in, time.Time{}, false, t0)
	if none.Verdict != Allow || none.Score != 0 {
		t.Errorf("no valid token: %+v", none)
	}
}

func TestScorePipeline(t *testing.T) {
	t.Parallel()
	set := scoreSettings()
	set.Action = ActionRefuse
	p := newPipe(t, set, nil)
	ctx := context.Background()
	subj := CommentSubject("e1")

	// Invalid timing token earns no points.
	if r := p.Score(ctx, ScoreInput{Subject: subj, Timing: "garbage", Texts: []string{"hi"}}); r.Verdict != Allow || r.Score != 0 {
		t.Errorf("invalid token: %+v", r)
	}
	// A token issued for another subject does not count either.
	other := p.IssueToken(CommentSubject("e2"))
	if r := p.Score(ctx, ScoreInput{Subject: subj, Timing: other, Texts: []string{"hi"}}); r.Score != 0 {
		t.Errorf("foreign token scored: %+v", r)
	}
	// A fresh token (issued "now", MinSeconds 3) is too fast; Refuse is never returned.
	fresh := p.IssueToken(subj)
	r := p.Score(ctx, ScoreInput{Subject: subj, Timing: fresh, Texts: []string{"viagra"}})
	if r.Verdict != Spam {
		t.Errorf("fresh token: %+v", r)
	}
	for _, in := range []ScoreInput{
		{Subject: subj, Timing: fresh, Texts: []string{"viagra привет"}},
		{Texts: []string{"viagra"}, Signals: []Signal{{ReasonBlocklist, PointsBlocklist}}},
	} {
		if v := p.Score(ctx, in).Verdict; v == Refuse || v == CaptchaFail {
			t.Errorf("Score returned %v", v)
		}
	}
}
