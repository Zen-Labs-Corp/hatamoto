package server

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/hooks"
)

// stubHosting is the smallest Hosting service the page renders with.
type stubHosting struct{ admin.HostingService }

func (stubHosting) View(context.Context) (admin.HostingView, error) {
	return admin.HostingView{Mode: "served"}, nil
}
func (stubHosting) Runs(context.Context, int) ([]admin.HostingRun, error)     { return nil, nil }
func (stubHosting) LiveRuns(context.Context, int) ([]admin.HostingRun, error) { return nil, nil }
func (stubHosting) Setup(context.Context) (admin.HostingSetup, error) {
	return admin.HostingSetup{}, nil
}
func (stubHosting) ProbeHostKey(context.Context) (string, error) { return "", nil }
func (stubHosting) TrustHostKey(context.Context, string) error   { return nil }
func (stubHosting) Islands() ([]admin.HostingIsland, []string)   { return nil, nil }

// setupStaticExport runs inside New, before the admin is mounted. The service
// it leaves on the server must reach the admin through the mount, or the
// Hosting page answers "unavailable" on every install.
func TestMountedAdminGetsHostingServiceSetBeforeMount(t *testing.T) {
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.EnableCookieAuth = true
	s.validateSession = fakeSession
	s.validateAPICookie = fakeAPISession
	s.hooks = hooks.New(s.logger)
	s.hostingSvc = stubHosting{} // what setupStaticExport stores
	s.setupRoutes()
	s.adminMu.Lock()
	got := s.adminDeps.Hosting
	s.adminMu.Unlock()
	if got == nil {
		t.Fatal("the admin was mounted without the Hosting service: its page would answer \"unavailable\"")
	}
}
