package supervisor

import (
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// Names of what can run.
const (
	RunBootstrap = "bootstrap"
	RunRelease   = "release"
)

// ChooseInput is everything Choose needs; the caller gathers it from disk so
// the decision stays a pure function.
type ChooseInput struct {
	WritableErr      error  // non-nil: releases dir not usable
	BootstrapSHA     string // sha256 of the bootstrap binary on disk now
	BootstrapVersion string
	Current          string // what releases/current points at ("" if none)
	CurrentOK        bool   // Current names a valid executable release
}

// Choice is the binary to run. Name is "bootstrap" or a version.
type Choice struct {
	Bootstrap bool
	Name      string
	Reason    string
}

// Choose decides which binary to run and updates st (Run, CurrentSeen,
// BootstrapSHA256) to match. Rules, in order:
//
//  1. releases dir unusable: bootstrap, st untouched.
//  2. bootstrap file changed since last seen (or first run): bootstrap wins.
//  3. current missing or invalid: bootstrap.
//  4. bootstrap won earlier and current is unchanged: stay on bootstrap; a
//     changed current (apply, or hand-flipped per RUNBOOK §8) is honoured.
//  5. a semver-newer bootstrap beats current; non-semver never counts as newer.
//  6. otherwise current.
func Choose(in ChooseInput, st *State) Choice {
	if in.WritableErr != nil {
		return Choice{Bootstrap: true, Name: RunBootstrap, Reason: "releases dir not writable: " + in.WritableErr.Error()}
	}
	if in.BootstrapVersion != "" {
		st.BootstrapVersion = in.BootstrapVersion
	}
	if in.BootstrapSHA != st.BootstrapSHA256 {
		st.BootstrapSHA256 = in.BootstrapSHA
		st.Run = RunBootstrap
		st.CurrentSeen = in.Current
		return Choice{Bootstrap: true, Name: RunBootstrap, Reason: "bootstrap binary changed"}
	}
	if !in.CurrentOK {
		return Choice{Bootstrap: true, Name: RunBootstrap, Reason: "no valid current release"}
	}
	if st.Run == RunBootstrap {
		if in.Current == st.CurrentSeen {
			return Choice{Bootstrap: true, Name: RunBootstrap, Reason: "bootstrap kept, current unchanged"}
		}
		st.Run = RunRelease
		st.CurrentSeen = in.Current
	}
	if release.ValidVersion(in.BootstrapVersion) && release.ValidVersion(in.Current) &&
		packages.Newer(in.BootstrapVersion, in.Current) {
		st.Run = RunBootstrap
		st.CurrentSeen = in.Current
		return Choice{Bootstrap: true, Name: RunBootstrap, Reason: "bootstrap is newer than current"}
	}
	st.Run = RunRelease
	st.CurrentSeen = in.Current
	return Choice{Name: in.Current, Reason: "current release"}
}
