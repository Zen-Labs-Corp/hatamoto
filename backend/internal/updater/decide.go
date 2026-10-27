package updater

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// pinSecurityReads is how many out-of-pin records a check reads, newest first,
// to find a security release that escapes the pin.
const pinSecurityReads = 2

var errReadBudget = fmt.Errorf("more than %d record reads in one check", MaxRecordFetches)

// MaxRecordFetches bounds how many records one decision may read.
const MaxRecordFetches = 5

// Actions a Decision can ask for.
const (
	ActionNone      = "none"
	ActionApply     = "apply"
	ActionRollback  = "rollback"
	ActionAvailable = "available" // a major release exists; never applied automatically
	ActionBlocked   = "blocked"
	ActionWait      = "wait"
)

// Decision kinds (the Status.Decision vocabulary that Decide produces).
const (
	KindUpToDate       = "up_to_date"
	KindScheduled      = "scheduled"
	KindWaitingWindow  = "waiting_window"
	KindWaitingDefer   = "waiting_defer"
	KindBlocked        = "blocked"
	KindAvailableMajor = "available_major"
	KindApplying       = "applying"
	KindRolledBackYank = "rolled_back_yanked"
	KindRolledBack     = "rolled_back" // the health gate rolled the newest release back
	KindDev            = "dev"
	KindBackupFailed   = "backup_failed" // the pre-update backup failed; the update was not installed
)

// Input is everything Decide looks at. It does no I/O of its own.
type Input struct {
	Running       string
	RunningRecord *release.Record // for rollback_floor; may be nil
	Manifest      *release.ChannelManifest
	Settings      Settings // effective: defaults and environment applied
	Now           time.Time
	Loc           *time.Location // site timezone for the window; nil = UTC
	Bucket        func(version string) int
	Bad           func(version string) bool   // versions that failed health gating; may be nil
	BadReason     func(version string) string // why a bad version failed; may be nil
	FirstSeen     map[string]time.Time
	// Previous is what a yank-back returns to: the supervisor's previous
	// release, or when PreviousIsBootstrap the bootstrap build's version
	// ("" or not semver when unknown). "" with PreviousIsBootstrap false: none.
	Previous            string
	PreviousIsBootstrap bool
	// Record reads and verifies one release record. Decide memoises it and
	// calls it at most MaxRecordFetches times.
	Record func(version string) (*release.Record, error)
}

// Decision is what to do about the channel.
type Decision struct {
	Action         string
	Kind           string
	Target         string
	Rec            *release.Record // set for apply and wait
	Reason         string
	AvailableMajor string
	FirstSeen      map[string]time.Time // input map plus newly seen candidates, pruned to the manifest
	// Fallback is the yank-back to use when this apply turns out not to be
	// installable (the preflight Decide cannot see). Set only for an apply that
	// replaces a withdrawn running release, and only when that yank-back is allowed.
	Fallback *Decision
}

// Decide picks a target. Order: a yank of the running version (move to its
// replacement or roll back), then candidates of the same major that are not
// yanked, bad or halted and whose bucket is inside the ramp, newest first;
// records are read lazily for min_from stepping, pin and kind; then defer and
// window. Security releases ignore defer, window and the pin (inside the
// major). Majors only ever yield ActionAvailable.
func Decide(in Input) (Decision, error) {
	if !release.ValidVersion(in.Running) {
		return Decision{Action: ActionNone, Kind: KindDev, Reason: "dev build, updates disabled"}, nil
	}
	if in.Manifest == nil {
		return Decision{}, errors.New("no channel manifest")
	}
	d := &decider{in: in, memo: map[string]recResult{}}
	return d.decide()
}

type recResult struct {
	rec *release.Record
	err error
}

type decider struct {
	in     Input
	memo   map[string]recResult
	calls  int
	yanked map[string]release.Yank
	// badNewest is the newest release newer than the running one that this
	// box rolled back before; with nothing else usable the status says so.
	badNewest string
}

func (d *decider) bad(v string) bool { return d.in.Bad != nil && d.in.Bad(v) }

// record returns the record for v, memoised, counting real fetches.
func (d *decider) record(v string) (*release.Record, error) {
	if r, ok := d.memo[v]; ok {
		return r.rec, r.err
	}
	if d.in.Record == nil {
		return nil, errors.New("no record source")
	}
	if d.calls >= MaxRecordFetches {
		return nil, errReadBudget
	}
	d.calls++
	rec, err := d.in.Record(v)
	if err == nil && (rec == nil || rec.Version != v) {
		got := ""
		if rec != nil {
			got = rec.Version
		}
		rec, err = nil, fmt.Errorf("record for %s names version %q", v, got)
	}
	d.memo[v] = recResult{rec, err}
	return rec, err
}

func (d *decider) decide() (Decision, error) {
	in := d.in
	d.yanked = map[string]release.Yank{}
	for _, y := range in.Manifest.Yanked {
		if release.ValidVersion(y.Version) {
			d.yanked[y.Version] = y
		}
	}

	var cands, outOfBucket []release.Entry // same major, newest first
	var major string                       // newest offered entry of another major
	seen := map[string]bool{}
	for _, e := range in.Manifest.Releases {
		if !release.ValidVersion(e.Version) || seen[e.Version] || !packages.Newer(e.Version, in.Running) {
			continue
		}
		seen[e.Version] = true
		_, y := d.yanked[e.Version]
		if y || e.Halted || d.bad(e.Version) {
			if !y && !e.Halted && SameMajor(e.Version, in.Running) && (d.badNewest == "" || packages.Newer(e.Version, d.badNewest)) {
				d.badNewest = e.Version
			}
			continue
		}
		if !SameMajor(e.Version, in.Running) {
			if major == "" || packages.Newer(e.Version, major) {
				major = e.Version
			}
			continue
		}
		pct := RampPct(e.Ramp, in.Now)
		if in.Bucket(e.Version) < pct {
			cands = append(cands, e)
		} else {
			outOfBucket = append(outOfBucket, e)
		}
	}
	newestFirst := func(a, b release.Entry) int { return packages.CompareVersions(b.Version, a.Version) }
	slices.SortFunc(cands, newestFirst)
	slices.SortFunc(outOfBucket, newestFirst)

	out := Decision{AvailableMajor: major, FirstSeen: d.firstSeen(cands)}

	if y, ok := d.yanked[in.Running]; ok {
		if r, done := d.yankTo(y, &out); done {
			return r, nil
		}
		// No usable replacement named. A normal newer release (a later fix)
		// wins over rolling back; anything short of installing it does not.
		back := d.yankBack(y, &out)
		if r, err := d.search(out, major, cands, outOfBucket); err == nil && r.Action == ActionApply {
			if back.Action == ActionRollback {
				r.Fallback = &back
			}
			return r, nil
		}
		return back, nil
	}
	return d.search(out, major, cands, outOfBucket)
}

// search walks the candidates newest first and returns the first that can be
// installed, else says why none can.
func (d *decider) search(out Decision, major string, cands, outOfBucket []release.Entry) (Decision, error) {
	in := d.in
	if in.Settings.Mode == ModeNotify {
		return d.notify(out, major, cands, outOfBucket), nil
	}
	var skipped []string
	var fetchErrs []error
	pinned := false
	var inPin, outPin []release.Entry // newest first
	for _, e := range cands {
		if PinMatches(in.Settings.Pin, e.Version) {
			inPin = append(inPin, e)
		} else {
			outPin = append(outPin, e)
		}
	}

	// eval reads e's record and says whether it can be installed now.
	const (
		usable = iota
		unusable
		needsStep // min_from is above the running version
		spent     // the read budget is used up
	)
	eval := func(e release.Entry) (*release.Record, int) {
		rec, err := d.record(e.Version)
		switch {
		case errors.Is(err, errReadBudget):
			return nil, spent
		case err != nil:
			fetchErrs = append(fetchErrs, err)
			skipped = append(skipped, fmt.Sprintf("%s: %v", e.Version, err))
			return nil, unusable
		case rec.Kind == "major":
			if major == "" || packages.Newer(e.Version, major) {
				major, out.AvailableMajor = e.Version, e.Version
			}
			return nil, unusable
		case release.ValidVersion(rec.MinFrom) && packages.CompareVersions(rec.MinFrom, in.Running) > 0:
			skipped = append(skipped, fmt.Sprintf("%s needs %s or newer first", e.Version, rec.MinFrom))
			return nil, needsStep
		}
		return rec, usable
	}

	// Outside the pin only a security release counts. It is looked for first,
	// within a small read allowance, so it wins over an in-pin release and
	// never starves one.
	for i, e := range outPin {
		pinned = true
		if i >= pinSecurityReads {
			break
		}
		rec, err := d.record(e.Version)
		if err != nil || rec.Kind == "major" || !rec.Security ||
			(release.ValidVersion(rec.MinFrom) && packages.CompareVersions(rec.MinFrom, in.Running) > 0) {
			continue
		}
		return d.finish(out, e.Version, rec), nil
	}

	// Newest first. The first release that needs a newer starting point shows
	// this box is behind; the stepping stone is then the oldest candidate, so
	// the rest are walked oldest first (one hop per check).
	next := 0
	for ; next < len(inPin); next++ {
		rec, v := eval(inPin[next])
		if v == usable {
			return d.finish(out, inPin[next].Version, rec), nil
		}
		if v == needsStep || v == spent {
			next++
			break
		}
	}
	for i := len(inPin) - 1; i >= next; i-- {
		if rec, v := eval(inPin[i]); v == usable {
			return d.finish(out, inPin[i].Version, rec), nil
		} else if v == spent {
			break
		}
	}
	if len(skipped) == 0 && next < len(inPin) {
		skipped = append(skipped, fmt.Sprintf("%d releases are newer than this box; too many to inspect in one check", len(inPin)))
	}
	switch {
	case len(skipped) > 0 && len(fetchErrs) == len(skipped) && len(fetchErrs) > 0:
		return out, fmt.Errorf("no usable release record: %w", errors.Join(fetchErrs...))
	case len(skipped) > 0:
		out.Action, out.Kind, out.Reason = ActionBlocked, KindBlocked, strings.Join(skipped, "; ")
	case pinned:
		out.Action, out.Kind = ActionNone, KindUpToDate
		out.Reason = fmt.Sprintf("pinned to %s; newer releases are outside the pin", in.Settings.Pin)
	case len(outOfBucket) > 0:
		e := outOfBucket[0]
		out.Action, out.Kind, out.Target = ActionNone, KindScheduled, e.Version
		out.Reason = fmt.Sprintf("%s is rolling out (bucket %d, ramp %d%%)", e.Version, in.Bucket(e.Version), RampPct(e.Ramp, in.Now))
	case major != "":
		out.Action, out.Kind, out.Target = ActionAvailable, KindAvailableMajor, major
		out.Reason = fmt.Sprintf("%s is a major release; apply it by hand", major)
	default:
		out = d.upToDate(out)
	}
	return out, nil
}

// notify is the decision of notify mode, from the manifest alone: it never
// reads a record, so a check is one GET. The newest same-major release inside
// the pin that is in this box's ramp is reported; min_from, kind and plugin
// checks happen only when it is installed.
func (d *decider) notify(out Decision, major string, cands, outOfBucket []release.Entry) Decision {
	in := d.in
	for _, e := range cands {
		if PinMatches(in.Settings.Pin, e.Version) {
			out.Action, out.Kind, out.Target = ActionNone, KindNotify, e.Version
			out.Reason = fmt.Sprintf("%s is available; mode notify, not installing", e.Version)
			return out
		}
	}
	switch {
	case len(cands) > 0:
		out.Action, out.Kind = ActionNone, KindUpToDate
		out.Reason = fmt.Sprintf("pinned to %s; newer releases are outside the pin", in.Settings.Pin)
	case len(outOfBucket) > 0:
		e := outOfBucket[0]
		out.Action, out.Kind, out.Target = ActionNone, KindScheduled, e.Version
		out.Reason = fmt.Sprintf("%s is rolling out (bucket %d, ramp %d%%)", e.Version, in.Bucket(e.Version), RampPct(e.Ramp, in.Now))
	case major != "":
		out.Action, out.Kind, out.Target = ActionAvailable, KindAvailableMajor, major
		out.Reason = fmt.Sprintf("%s is a major release; apply it by hand", major)
	default:
		out = d.upToDate(out)
	}
	return out
}

// upToDate is the status when nothing newer is usable: up to date, unless the
// reason is that this box rolled the newest release back.
func (d *decider) upToDate(out Decision) Decision {
	out.Action, out.Kind = ActionNone, KindUpToDate
	out.Reason = "running the newest release offered"
	if v := d.badNewest; v != "" {
		out.Kind, out.Target = KindRolledBack, v
		out.Reason = "failed its health gate here and is not retried"
		if d.in.BadReason != nil {
			if why := d.in.BadReason(v); why != "" {
				out.Reason = why
			}
		}
	}
	return out
}

// finish applies defer and window to the chosen target.
func (d *decider) finish(out Decision, target string, rec *release.Record) Decision {
	in := d.in
	out.Target, out.Rec = target, rec
	if !rec.Security {
		if days := in.Settings.DeferDays; days > 0 {
			if until := out.FirstSeen[target].AddDate(0, 0, days); in.Now.Before(until) {
				out.Action, out.Kind = ActionWait, KindWaitingDefer
				out.Reason = fmt.Sprintf("%s is deferred until %s", target, until.UTC().Format(time.RFC3339))
				return out
			}
		}
		if !InWindow(in.Now, in.Loc, in.Settings.WindowStart, in.Settings.WindowEnd) {
			out.Action, out.Kind = ActionWait, KindWaitingWindow
			out.Reason = fmt.Sprintf("%s waits for the maintenance window", target)
			return out
		}
	}
	out.Action, out.Kind = ActionApply, KindApplying
	out.Reason = fmt.Sprintf("updating %s to %s", in.Running, target)
	return out
}

// yankTo handles a yank of the running version that names a replacement:
// move to it when usable (ignoring ramp, window, defer and pin). done is false
// when there is no usable replacement.
func (d *decider) yankTo(y release.Yank, out *Decision) (r Decision, done bool) {
	in := d.in
	if y.To == "" || !release.ValidVersion(y.To) || !packages.Newer(y.To, in.Running) || d.bad(y.To) {
		return Decision{}, false
	}
	if _, yankedToo := d.yanked[y.To]; yankedToo {
		return Decision{}, false
	}
	if e, ok := d.entry(y.To); !ok || e.Halted {
		return Decision{}, false
	}
	r = *out
	if !SameMajor(y.To, in.Running) {
		r.Action, r.Kind, r.Target, r.AvailableMajor = ActionAvailable, KindAvailableMajor, y.To, y.To
		r.Reason = fmt.Sprintf("%s was withdrawn (%s); its replacement %s is a major release", in.Running, y.Reason, y.To)
		return r, true
	}
	if in.Settings.Mode == ModeNotify {
		r.Action, r.Kind, r.Target = ActionNone, KindNotify, y.To
		r.Reason = fmt.Sprintf("%s was withdrawn (%s); its replacement %s is available; mode notify, not installing", in.Running, y.Reason, y.To)
		return r, true
	}
	rec, err := d.record(y.To)
	switch {
	case err != nil:
		return Decision{}, false
	case release.ValidVersion(rec.MinFrom) && packages.CompareVersions(rec.MinFrom, in.Running) > 0:
		r.Action, r.Kind, r.Target = ActionBlocked, KindBlocked, y.To
		r.Reason = fmt.Sprintf("%s was withdrawn (%s); replacement %s needs %s first", in.Running, y.Reason, y.To, rec.MinFrom)
	default:
		r.Action, r.Kind, r.Target, r.Rec = ActionApply, KindApplying, y.To, rec
		r.Reason = fmt.Sprintf("%s was withdrawn (%s); moving to %s", in.Running, y.Reason, y.To)
	}
	return r, true
}

// yankBack decides the rollback after a yank of the running version. The
// previous release, or the bootstrap build when the first update came from
// it, is the target, when it is at or above the running release's rollback
// floor and neither withdrawn nor on the bad list. A bootstrap build whose
// version is not semver (or unknown) cannot be compared with a floor, so it is
// a valid target only when the running release sets no floor.
func (d *decider) yankBack(y release.Yank, out *Decision) Decision {
	in := d.in
	r := *out
	why := y.Reason
	r.Target = in.Previous
	block := func(format string, a ...any) Decision {
		r.Action, r.Kind = ActionBlocked, KindBlocked
		r.Reason = fmt.Sprintf("%s was withdrawn (%s) %s", in.Running, why, fmt.Sprintf(format, a...))
		return r
	}
	prev := in.Previous
	if in.PreviousIsBootstrap {
		if !release.ValidVersion(prev) {
			r.Target = "bootstrap"
		}
		prev = "the bootstrap build " + prev
		if strings.TrimSpace(in.Previous) == "" {
			prev = "the bootstrap build"
		}
	}
	switch {
	case !in.PreviousIsBootstrap && !release.ValidVersion(in.Previous):
		return block("and there is no previous release to return to")
	case in.RunningRecord == nil:
		return block("but its record is unavailable to check the rollback floor")
	}
	if floor := in.RunningRecord.RollbackFloor; floor != "" {
		switch {
		case !release.ValidVersion(in.Previous):
			return block("but %s has no comparable version to check against its rollback floor %s", prev, floor)
		case packages.CompareVersions(in.Previous, floor) < 0:
			return block("but %s is older than its rollback floor %s", prev, floor)
		}
	}
	if _, yanked := d.yanked[in.Previous]; yanked && release.ValidVersion(in.Previous) {
		return block("but %s was withdrawn too", prev)
	}
	if d.bad(in.Previous) || (in.PreviousIsBootstrap && d.bad("bootstrap")) {
		return block("but %s is on this box's bad list (it failed before)", prev)
	}
	r.Action, r.Kind = ActionRollback, KindRolledBackYank
	r.Reason = fmt.Sprintf("%s was withdrawn (%s); rolling back to %s", in.Running, why, prev)
	return r
}

func (d *decider) entry(v string) (release.Entry, bool) {
	for _, e := range d.in.Manifest.Releases {
		if e.Version == v {
			return e, true
		}
	}
	return release.Entry{}, false
}

// firstSeen returns the input map plus every current candidate stamped with
// Now when new, dropping versions the manifest no longer lists.
func (d *decider) firstSeen(cands []release.Entry) map[string]time.Time {
	listed := map[string]bool{}
	for _, e := range d.in.Manifest.Releases {
		listed[e.Version] = true
	}
	out := map[string]time.Time{}
	for v, t := range d.in.FirstSeen {
		if listed[v] {
			out[v] = t
		}
	}
	for _, e := range cands {
		if _, ok := out[e.Version]; !ok {
			out[e.Version] = d.in.Now
		}
	}
	return out
}

// InWindow reports whether now falls inside the maintenance window, hours
// 0-23 in loc (UTC when nil). Either bound nil, or start == end, means any
// time; start > end wraps midnight. The window is [start, end).
func InWindow(now time.Time, loc *time.Location, start, end *int) bool {
	if start == nil || end == nil || *start == *end {
		return true
	}
	if loc == nil {
		loc = time.UTC
	}
	h := now.In(loc).Hour()
	if *start < *end {
		return h >= *start && h < *end
	}
	return h >= *start || h < *end
}
