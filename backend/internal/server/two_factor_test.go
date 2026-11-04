package server

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/twofactor"
)

func TestPasskeyRPNeedsASiteAddress(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: cfgWithBase("")}
	if _, err := s.passkeyRP(context.Background()); !errors.Is(err, twofactor.ErrNoRelyingParty) {
		t.Fatalf("no address: %v", err)
	}
	s = &Server{cfg: cfgWithBase("https://www.example.com/")}
	rp, err := s.passkeyRP(context.Background())
	if err != nil || rp.ID != "www.example.com" || len(rp.Origins) != 1 || rp.Origins[0] != "https://www.example.com" {
		t.Fatalf("rp = %+v, %v", rp, err)
	}
}

func TestSudoFactorMapsWrongCode(t *testing.T) {
	t.Parallel()

	var _ admin.SecondFactor = sudoFactor{}
}

func cfgWithBase(base string) *config.Config {
	return &config.Config{PublicSite: config.PublicSiteConfig{BaseURL: base}}
}
