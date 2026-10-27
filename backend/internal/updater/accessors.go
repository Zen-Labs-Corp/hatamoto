package updater

import "hatamoto.dev/core/internal/supervisor"

// Store returns the policy and status store the Updater was built with (nil
// for the offline CLI). The admin reads and saves settings through it.
func (u *Updater) Store() *Store { return u.env.Store }

// Version returns the version this Updater was built to run as.
func (u *Updater) Version() string { return u.env.Version }

// RunningVersion is the version the supervisor runs now, which after a
// self-update or a rollback is the current release and not the version this
// process was stamped with. It falls back to Version when the state cannot be
// read.
func (u *Updater) RunningVersion() string {
	layout := supervisor.NewLayout(u.env.Data)
	state, err := supervisor.LoadState(layout.StatePath())
	if err != nil {
		return u.env.Version
	}
	return supervisor.RunningVersion(layout, state, u.env.Version)
}
