// Package designcheck runs core's admin design rules on a module's
// templates. Call Check from a test of the module; it is a separate package so
// the lint code is linked into a binary only when imported.
package designcheck

import (
	"errors"
	"io/fs"

	idc "hatamoto.dev/core/internal/admin/designcheck"
)

// Check returns nil when every .html file in fsys follows core's template
// rules (UI-01, UI-02, UI-03, UI-05, UI-07, UI-08, UI-09, UI-10) and MOD-01:
// only classes that core's admin CSS defines and no inline style. Otherwise it
// returns one joined error per violation, each as "file:line RULE message".
// fsys is the module's Page.Templates root, so files are pages/<name>.html.
func Check(fsys fs.FS) error {
	vs, err := idc.CheckModule(fsys)
	if err != nil {
		return err
	}
	errs := make([]error, len(vs))
	for i, v := range vs {
		errs[i] = errors.New(v.String())
	}
	return errors.Join(errs...)
}
