package localeconv

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type pgService struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewService returns the Postgres-backed Service.
func NewService(pool *pgxpool.Pool, logger *slog.Logger) Service {
	return &pgService{pool: pool, logger: logger}
}

func (s *pgService) Suggest(context.Context) (*Plan, []Issue, error) {
	return nil, nil, ErrNotImplemented
}

func (s *pgService) DryRun(context.Context, *Plan) (*Report, error) {
	return nil, ErrNotImplemented
}

func (s *pgService) Apply(context.Context, *Plan, ApplyOpts) (*Record, *Report, error) {
	return nil, nil, ErrNotImplemented
}

func (s *pgService) Revert(context.Context, int64, bool, string) (*RevertResult, error) {
	return nil, ErrNotImplemented
}

func (s *pgService) History(context.Context) ([]HistoryItem, error) {
	return nil, ErrNotImplemented
}
