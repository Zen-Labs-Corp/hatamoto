package admin

import (
	"context"
	"sync/atomic"
	"testing"
)

// countingBackups counts List calls on top of fakeBackups.
type countingBackups struct {
	*fakeBackups
	lists atomic.Int32
}

func (c *countingBackups) List(ctx context.Context) (BackupsState, error) {
	c.lists.Add(1)
	return c.fakeBackups.List(ctx)
}

func TestDashboardReadsBackupsOncePerBuild(t *testing.T) {
	t.Parallel()
	cb := &countingBackups{fakeBackups: &fakeBackups{state: BackupsState{
		FreeBytes: 50 << 30,
		Rows:      []BackupInfo{{State: BackupOK, Created: analyticsNow()}},
	}}}
	g := newRig(t, func(d *Deps) { d.Backups = cb })
	g.a.buildDash(context.Background(), g.a.dashJobs(roleViewer("owner")))
	if n := cb.lists.Load(); n != 1 {
		t.Errorf("Backups.List called %d times for one Overview build, want 1", n)
	}
}
