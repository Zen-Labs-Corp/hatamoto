package commentsubmit

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestLimitsAllowIPBurst(t *testing.T) {
	l := NewLimits(nil)
	defer l.Stop()
	for i := 0; i < 3; i++ {
		if !l.Allow("ip", "net", "mail", "ent") {
			t.Fatalf("request %d dropped inside the burst", i+1)
		}
	}
	if l.Allow("ip", "net", "mail", "ent") {
		t.Fatal("4th request from one ip passed")
	}
	if !l.Allow("other", "net2", "mail2", "ent2") {
		t.Fatal("unrelated keys dropped")
	}
}

func TestLimitsSlowMode(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimits(c.now)
	defer l.Stop()
	iv := 120 * time.Second
	if l.InSlowMode("u", "e", iv) {
		t.Fatal("slow before any post")
	}
	l.MarkPosted("u", "e")
	c.t = c.t.Add(iv - time.Second)
	if !l.InSlowMode("u", "e", iv) {
		t.Fatal("not slow at interval-1s")
	}
	if l.InSlowMode("u", "other", iv) {
		t.Fatal("slow on another entity")
	}
	if l.InSlowMode("u", "e", 0) {
		t.Fatal("slow with interval 0")
	}
	c.t = c.t.Add(time.Second)
	if l.InSlowMode("u", "e", iv) {
		t.Fatal("still slow at interval")
	}
}

func TestSlowModeBounded(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	s := &slowMode{now: c.now, last: map[string]time.Time{}}
	for i := 0; i < slowModeCap+5; i++ {
		c.t = c.t.Add(time.Second)
		s.mark(string(rune('a'+i%26)) + time.Duration(i).String())
	}
	if len(s.last) != slowModeCap {
		t.Fatalf("size %d, want %d", len(s.last), slowModeCap)
	}
}

func TestSurge(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimits(c.now)
	defer l.Stop()
	s := l.Surge()
	for i := 1; i <= defaultSurgeLimit; i++ {
		if a, _ := s.Note(); a {
			t.Fatalf("active at note %d", i)
		}
		c.t = c.t.Add(time.Second)
	}
	a, st := s.Note()
	if !a || !st {
		t.Fatalf("31st note: active=%v started=%v", a, st)
	}
	a, st = s.Note()
	if !a || st {
		t.Fatalf("32nd note: active=%v started=%v", a, st)
	}
	c.t = c.t.Add(surgeWindow)
	if a, _ := s.Note(); a {
		t.Fatal("active after the window")
	}
}
