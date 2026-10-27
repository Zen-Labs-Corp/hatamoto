package supervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// switchTo runs the pending apply or rollback in state.json: record the
// snapshot, swap the links, stop the old child, start the new one and gate it.
// Every step is safe to repeat, so the same call finishes the job after a
// restart: swaps are skipped when current already names the target and the
// snapshot taken by the first pass is reused.
//
// It returns errStopped when a stop request interrupted it; the pending entry
// is then left in place for the next start. Any other outcome (success,
// rolled back, refused) clears it.
func (s *sup) switchTo(ctx context.Context) error {
	var p Pending
	var from, target string
	s.log.Info("switch starting", "event", "switch")

	var refused string
	err := s.update(func(st *State) error {
		if st.Pending == nil {
			return nil
		}
		p = *st.Pending
		target = p.Version
		switch {
		case p.Kind == "apply" && st.IsBad(target):
			refused = "version is on the bad list"
		case target != RunBootstrap && !s.layout.Valid(target):
			refused = "staged release is missing or not executable"
		case p.Kind != "apply" && p.Kind != "rollback":
			refused = "unknown pending kind " + p.Kind
		case target != RunBootstrap:
			if verr := s.verifyRelease(target); verr != nil {
				refused = "staged release failed verification: " + verr.Error()
				st.AddBad(target, refused)
			}
		}
		if refused != "" {
			st.Pending, st.Phase, st.LastError = nil, "idle", refused
			return nil
		}
		if p.Before == nil {
			p.Before = snapshotOf(s.layout, st)
			p.PrevBefore = p.Before.Previous
			p.StartedAt = time.Now().UTC()
			st.Attempts++
		}
		from = fromName(p.Before)
		st.Phase = "probation"
		st.Pending = &p
		return nil // pending is saved before any link moves
	})
	if err != nil {
		s.log.Warn("switch aborted, cannot update state", "event", "switch", "err", err)
		return nil
	}
	if p.Version == "" && refused == "" { // nothing was pending
		return nil
	}
	if refused != "" {
		s.log.Warn("switch refused", "event", "switch", "to", target, "reason", refused)
		s.event(Event{From: fromNameOr(p), To: target, Phase: PhaseFailed, Error: refused,
			StartedAt: p.RequestedAt, FinishedAt: time.Now().UTC()})
		return nil
	}

	if err := s.update(func(st *State) error { return swapLinks(s.layout, st, &p) }); err != nil {
		return s.abortSwitch(p, from, target, "cannot swap release links: "+err.Error())
	}
	s.refreshBaseline(ctx)
	s.stopChild()
	if ctx.Err() != nil { // a stop arrived while the old child was going down
		s.log.Info("switch interrupted by stop, will resume at next start", "event", "switch", "to", target)
		return errStopped
	}
	s.log.Info("switch: running target under gate", "event", "switch", "from", from, "to", target, "kind", p.Kind)

	reason := s.probation(ctx, target)
	switch {
	case errors.Is(reason, errStopped):
		s.log.Info("switch interrupted by stop, will resume at next start", "event", "switch", "to", target)
		return errStopped
	case reason == nil:
		s.finishSwitch(p, from, target)
		return nil
	default:
		s.rollback(p, from, target, reason.Error())
		return nil
	}
}

// probation starts the target and gates it. A child that dies is restarted
// with backoff; the third boot-window crash fails the gate, as does any failed
// check. nil means healthy.
func (s *sup) probation(ctx context.Context, target string) error {
	choice := Choice{Name: target, Reason: "switch target"}
	if target == RunBootstrap {
		choice.Bootstrap = true
	}
	s.crashes.reset()
	retriedSmoke := false
	for {
		if ctx.Err() != nil {
			return errStopped // never start a child once a stop was requested
		}
		s.startChild(choice)
		g := GateConfig{
			ReadyURL:          s.cfg.ReadyURL,
			SmokeAddr:         s.smokeAddr,
			Token:             s.token,
			Started:           s.childStart,
			ReadyTimeout:      s.cfg.ReadyTimeout,
			ObserveWindow:     s.cfg.ObserveWindow,
			SmokePoll:         s.cfg.SmokePoll,
			KnownPluginErrors: s.st.PluginErrors,
			Baseline:          s.st.SmokeBaseline,
			StaleBaseline:     time.Since(s.st.BaselineAt) > baselineMaxAge,
			RenderBaseline:    s.st.RenderBaseline,
			BootFaultBaseline: s.st.BootFaultBaseline,
		}
		res, err := s.cfg.gate(ctx, g, s.child.Done())
		if err == nil {
			s.pluginErrors, s.baseline, s.renderFirst, s.bootFaults = res.PluginErrors, BaselineOf(res), res.FirstRender, res.BootFaults
			return nil
		}
		if ctx.Err() != nil {
			return errStopped
		}
		if errors.Is(err, errSmokeUnreachable) && !retriedSmoke {
			// The probed smoke port may have been taken by another process
			// between probing and the child's bind. Try once more with a new one.
			retriedSmoke = true
			s.log.Warn("smoke endpoint unreachable, restarting the child once with a new smoke port",
				"event", "gate", "version", target, "reason", err)
			s.stopChild()
			continue
		}
		if !errors.Is(err, errChildExited) {
			s.log.Warn("gate failed", "event", "gate", "version", target, "reason", err)
			return err
		}
		n := s.crashes.record(time.Since(s.childStart))
		s.log.Warn("child exited during gate", "event", "crash", "version", target, "consecutive", n, "err", s.child.Err())
		s.child = nil
		if s.crashes.looping() {
			s.crashes.reset()
			return fmt.Errorf("crash loop: %d crashes within the boot window", crashLoopCount)
		}
		if !s.sleep(ctx, backoff(s.cfg.BackoffBase, n)) {
			return errStopped
		}
	}
}

// finishSwitch records a healthy target.
func (s *sup) finishSwitch(p Pending, from, target string) {
	phase := PhaseSuccess
	err := s.update(func(st *State) error {
		st.PluginErrors, st.SmokeBaseline, st.RenderBaseline, st.BootFaultBaseline = s.pluginErrors, s.baseline, s.renderFirst, s.bootFaults
		st.BaselineAt = time.Now().UTC()
		st.Phase, st.Pending, st.LastError, st.Unproven = "idle", nil, "", ""
		if p.Kind == "rollback" && p.Before.Run == RunRelease && p.Before.Current != "" {
			phase = PhaseManualRollback
			st.AddBad(p.Before.Current, "manual rollback")
		}
		if p.Kind == "apply" && target != RunBootstrap {
			st.Unproven = target // gate passed; proven once it has lived the boot window
		}
		return nil
	})
	if err != nil {
		s.log.Warn("cannot record switch outcome", "event", "switch", "err", err)
	}
	s.armProven()
	s.log.Info("switch finished, gate passed", "event", "gate", "from", from, "to", target, "phase", phase)
	s.event(Event{From: from, To: target, Phase: phase, StartedAt: p.StartedAt, FinishedAt: time.Now().UTC()})
	s.crashes.reset()
}

// armProven makes the running child's release proven once it has lived the
// boot window, the span in which a crash loop still rolls back.
func (s *sup) armProven() {
	if s.provenTimer != nil {
		s.provenTimer.Stop()
		s.provenTimer, s.provenC = nil, nil
	}
	if !s.writable || s.child == nil || s.st.Unproven == "" || s.st.Unproven != s.childName {
		return
	}
	s.provenChild = s.child
	s.provenTimer = time.NewTimer(max(0, s.cfg.BootWindow-time.Since(s.childStart)))
	s.provenC = s.provenTimer.C
}

// markProven runs when the proven timer fires.
func (s *sup) markProven() {
	s.provenTimer, s.provenC = nil, nil
	if s.child == nil || s.child != s.provenChild {
		return
	}
	select {
	case <-s.child.Done():
		return // it died in the same instant: that is a crash, not a proof
	default:
	}
	name := s.childName
	err := s.update(func(st *State) error {
		if st.Unproven == name {
			st.Unproven = ""
		}
		return nil
	})
	if err != nil {
		s.log.Warn("cannot record release as proven", "event", "gate", "err", err)
		return
	}
	s.log.Info("release proven: lived the boot window", "event", "gate", "version", name)
}

// rollback restores the snapshot taken before the switch, bad-lists a failed
// apply (never a failed manual rollback) and restarts the old binary with no
// second gate, so there is no ping-pong. It never touches the database.
func (s *sup) rollback(p Pending, from, target, reason string) {
	s.stopChild()
	phase := PhaseRolledBack
	err := s.update(func(st *State) error {
		if err := restoreSnapshot(s.layout, st, p.Before); err != nil {
			return err
		}
		if p.Kind == "apply" {
			st.AddBad(target, reason)
		} else {
			phase = PhaseFailed
		}
		st.Phase, st.Pending, st.LastError, st.Unproven = "idle", nil, reason, ""
		st.Ungated = nil
		return nil
	})
	if err != nil {
		s.log.Error("cannot restore release links after failed switch", "event", "rollback", "err", err)
	}
	s.log.Warn("rollback: switch failed its gate, restored previous binary", "event", "rollback",
		"from", from, "failed", target, "reason", reason)
	s.event(Event{From: from, To: target, Phase: phase, Error: reason, StartedAt: p.StartedAt, FinishedAt: time.Now().UTC()})
	s.crashes.reset()
}

// abortSwitch ends a switch that could not even move the links.
func (s *sup) abortSwitch(p Pending, from, target, reason string) error {
	_ = s.update(func(st *State) error {
		st.Phase, st.Pending, st.LastError = "idle", nil, reason
		return nil
	})
	s.log.Warn("switch aborted", "event", "switch", "to", target, "reason", reason)
	s.event(Event{From: from, To: target, Phase: PhaseFailed, Error: reason, StartedAt: p.StartedAt, FinishedAt: time.Now().UTC()})
	return nil
}

// crashLoopRollback handles a crash loop with no switch in flight. It rolls
// back only a release that never passed a gate (st.Unproven names it): that
// is a switch still in its boot window. A proven release that crashes, say
// because the database is down at boot, keeps restarting with backoff and is
// never bad-listed. The target is previous unless that is bad-listed, else
// the bootstrap. False when nothing was rolled back.
func (s *sup) crashLoopRollback(name string) bool {
	if name == RunBootstrap || !s.writable {
		return false
	}
	var to string
	err := s.update(func(st *State) error {
		if st.Unproven != name {
			return errNotUnproven
		}
		cur := s.layout.Link("current")
		if cur != name {
			return errors.New("running version is not current")
		}
		prev := s.layout.Link("previous")
		switch {
		case s.layout.Valid(prev) && !st.IsBad(prev):
			to = prev
			if err := s.layout.SwapSymlink("current", prev); err != nil {
				return err
			}
			if err := s.layout.RemoveLink("previous"); err != nil {
				return err
			}
			st.Run, st.CurrentSeen = RunRelease, prev
		default: // previous_is_bootstrap, nothing, or a bad previous: the bootstrap is the last resort
			to = RunBootstrap
			st.Run, st.CurrentSeen = RunBootstrap, cur
		}
		st.PreviousIsBootstrap = false
		st.Unproven = ""
		st.AddBad(name, "crash loop")
		st.LastError = "crash loop"
		return nil
	})
	if errors.Is(err, errNotUnproven) {
		return false
	}
	if err != nil {
		s.log.Warn("cannot roll back crash loop", "event", "rollback", "version", name, "err", err)
		return false
	}
	s.log.Warn("rollback: crash loop, restored previous binary", "event", "rollback", "from", name, "to", to)
	now := time.Now().UTC()
	s.event(Event{From: name, To: to, Phase: PhaseRolledBack, Error: "crash loop", StartedAt: now, FinishedAt: now})
	return true
}

var errNotUnproven = errors.New("release already passed a gate")

// snapshot captures the links and run choice before a switch.
func snapshotOf(l Layout, st *State) *LinkSnapshot {
	return &LinkSnapshot{
		Current:             l.Link("current"),
		Previous:            l.Link("previous"),
		PreviousIsBootstrap: st.PreviousIsBootstrap,
		Run:                 st.Run,
		CurrentSeen:         st.CurrentSeen,
	}
}

// swapLinks moves the links for p, using the snapshot in p.Before. The old
// version becomes previous (or previous_is_bootstrap when the bootstrap was
// running), then current points at the target. Steps that are already done
// are skipped, so a replay is harmless. A rollback to the bootstrap leaves
// current alone: current_seen equals it, so Choose keeps the bootstrap.
func swapLinks(l Layout, st *State, p *Pending) error {
	b := p.Before
	if p.Version == RunBootstrap {
		if err := l.RemoveLink("previous"); err != nil {
			return err
		}
		st.Run, st.CurrentSeen, st.PreviousIsBootstrap = RunBootstrap, b.Current, false
		return nil
	}
	if l.Link("current") != p.Version {
		if b.Run == RunRelease && b.Current != "" {
			if err := l.SwapSymlink("previous", b.Current); err != nil {
				return err
			}
			st.PreviousIsBootstrap = false
		} else {
			if err := l.RemoveLink("previous"); err != nil {
				return err
			}
			st.PreviousIsBootstrap = true
		}
		if err := l.SwapSymlink("current", p.Version); err != nil {
			return err
		}
	}
	st.Run, st.CurrentSeen = RunRelease, p.Version
	return nil
}

// restoreSnapshot puts the links and run choice back as they were.
func restoreSnapshot(l Layout, st *State, b *LinkSnapshot) error {
	for _, ln := range []struct{ name, target string }{{"current", b.Current}, {"previous", b.Previous}} {
		var err error
		if ln.target == "" {
			err = l.RemoveLink(ln.name)
		} else {
			err = l.SwapSymlink(ln.name, ln.target)
		}
		if err != nil {
			return err
		}
	}
	st.PreviousIsBootstrap, st.Run, st.CurrentSeen = b.PreviousIsBootstrap, b.Run, b.CurrentSeen
	return nil
}

func fromName(b *LinkSnapshot) string {
	if b.Run == RunRelease && b.Current != "" {
		return b.Current
	}
	return RunBootstrap
}

func fromNameOr(p Pending) string {
	if p.Before != nil {
		return fromName(p.Before)
	}
	return strings.TrimSpace(p.From)
}
