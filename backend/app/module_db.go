package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/modhost"
)

// publishModulePool hands p to compiled-in modules (module/db) and returns the
// function that takes it back. Defer that function after the pool's Close so
// it runs first.
func publishModulePool(p *pgxpool.Pool) (clear func()) {
	modhost.SetPool(p)
	return func() { modhost.SetPool(nil) }
}
