package database

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/config"
)

func parsePool(t *testing.T) *pgxpool.Config {
	t.Helper()
	pc, err := pgxpool.ParseConfig("postgres://u@localhost/db")
	if err != nil {
		t.Fatal(err)
	}
	return pc
}

func TestApplyPoolConfigWarmPoolChecksLess(t *testing.T) {
	pc := parsePool(t)
	applyPoolConfig(pc, config.DatabaseConfig{
		MaxConnections: 20, MinConnections: 5,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if pc.HealthCheckPeriod != 5*time.Minute {
		t.Fatalf("HealthCheckPeriod = %v, want 5m", pc.HealthCheckPeriod)
	}
	if pc.MaxConns != 20 || pc.MinConns != 5 || pc.MaxConnLifetime != time.Hour || pc.MaxConnIdleTime != 30*time.Minute {
		t.Fatalf("settings not copied: %+v", pc)
	}
}

func TestApplyPoolConfigDrainingPoolKeepsDefault(t *testing.T) {
	pc := parsePool(t)
	applyPoolConfig(pc, config.DatabaseConfig{MaxConnections: 20, MinConnections: 0})
	if want := parsePool(t).HealthCheckPeriod; pc.HealthCheckPeriod != want {
		t.Fatalf("HealthCheckPeriod = %v, want pgx default %v", pc.HealthCheckPeriod, want)
	}
}
