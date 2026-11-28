package server

import (
	"context"
	"errors"

	"hatamoto.dev/core/internal/admin"
)

// cspMode values: cspModeCfg follows cfg.Security.CSPEnforce (test shells, the
// time before the stored setting loads, CSP_ENFORCE set, or no stored choice).
const (
	cspModeCfg int32 = iota
	cspModeReport
	cspModeEnforce
)

var _ admin.CSPService = (*Server)(nil)

// cspEnforcing reports whether public responses get an enforcing CSP header.
func (s *Server) cspEnforcing() bool {
	switch s.cspMode.Load() {
	case cspModeEnforce:
		return true
	case cspModeReport:
		return false
	}
	return s.cfg.Security.CSPEnforce
}

// applyStoredCSP applies the stored choice unless CSP_ENFORCE was set explicitly.
func (s *Server) applyStoredCSP(stored *bool) {
	switch {
	case s.cfg.Security.CSPEnforceSet || stored == nil:
		s.cspMode.Store(cspModeCfg)
	case *stored:
		s.cspMode.Store(cspModeEnforce)
	default:
		s.cspMode.Store(cspModeReport)
	}
}

// CSPState implements admin.CSPService.
func (s *Server) CSPState() admin.CSPState {
	return admin.CSPState{Enforcing: s.cspEnforcing(), EnvForced: s.cfg.Security.CSPEnforceSet}
}

// EnforceCSP stores "enforce" and switches this process at once.
func (s *Server) EnforceCSP(ctx context.Context) error {
	if s.siteConfig == nil {
		return errors.New("site settings not available")
	}
	if err := s.siteConfig.SetCSPEnforce(ctx, true); err != nil {
		return err
	}
	on := true
	s.applyStoredCSP(&on)
	return nil
}
