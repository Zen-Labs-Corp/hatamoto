package server

import (
	"testing"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/siteconfig"
)

const (
	hdrEnforce = "Content-Security-Policy"
	hdrReport  = "Content-Security-Policy-Report-Only"
)

func cspServer(enforce, set bool) *Server {
	cfg := &config.Config{}
	cfg.Security.CSPEnforce = enforce
	cfg.Security.CSPEnforceSet = set
	return &Server{cfg: cfg}
}

func boolp(b bool) *bool { return &b }

func baseCSPName(s *Server) string {
	for _, h := range s.baseSecurityHeaders() {
		if h.Name == hdrEnforce || h.Name == hdrReport {
			return h.Name
		}
	}
	return ""
}

func TestCSPModeDefaultEnforces(t *testing.T) {
	t.Parallel()
	s := cspServer(true, false)
	s.applyStoredCSP(nil)
	if got := baseCSPName(s); got != hdrEnforce {
		t.Fatalf("default header = %q", got)
	}
}

func TestCSPModeStoredChoiceSwitchesLive(t *testing.T) {
	t.Parallel()
	s := cspServer(true, false)
	s.applyStoredCSP(boolp(false))
	if got := baseCSPName(s); got != hdrReport {
		t.Fatalf("stored false = %q", got)
	}
	s.applyStoredCSP(boolp(true))
	if got := baseCSPName(s); got != hdrEnforce {
		t.Fatalf("stored true = %q", got)
	}
}

func TestCSPModeEnvBeatsStored(t *testing.T) {
	t.Parallel()
	off := cspServer(false, true)
	off.applyStoredCSP(boolp(true))
	if got := baseCSPName(off); got != hdrReport {
		t.Fatalf("env false + stored true = %q", got)
	}
	on := cspServer(true, true)
	on.applyStoredCSP(boolp(false))
	if got := baseCSPName(on); got != hdrEnforce {
		t.Fatalf("env true + stored false = %q", got)
	}
}

func TestCSPModeState(t *testing.T) {
	t.Parallel()
	if st := cspServer(false, true).CSPState(); !st.EnvForced || st.Enforcing {
		t.Fatalf("env-forced report-only: %+v", st)
	}
	if st := cspServer(true, false).CSPState(); st.EnvForced || !st.Enforcing {
		t.Fatalf("default: %+v", st)
	}
}

func TestCSPModeZeroShellFollowsCfg(t *testing.T) {
	t.Parallel()
	if got := baseCSPName(cspServer(false, false)); got != hdrReport {
		t.Fatalf("zero shell = %q", got)
	}
}

func TestCSPModePageCacheApplyPassesStored(t *testing.T) {
	t.Parallel()
	r := newServiceRig(t, false, "https://x.test")
	var got []*bool
	r.svc.setCSP = func(b *bool) { got = append(got, b) }
	r.svc.Apply(siteconfig.Config{PageCacheEnabled: true, CSPEnforce: boolp(false)})
	if len(got) != 1 || got[0] == nil || *got[0] {
		t.Fatalf("setCSP args = %v", got)
	}
}
