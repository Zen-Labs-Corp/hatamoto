package updater

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type sqlSpy struct{ sql string }

func (s *sqlSpy) QueryRow(context.Context, string, ...any) pgx.Row { return noRow{} }
func (s *sqlSpy) Exec(_ context.Context, q string, _ ...any) (pgconn.CommandTag, error) {
	s.sql = q
	return pgconn.CommandTag{}, nil
}

func TestSavePolicyNamesOnlyItsColumns(t *testing.T) {
	spy := &sqlSpy{}
	st := NewStore(spy)
	for cols, want := range map[string][]string{
		ColMode:                  {"mode"},
		ColMode + "," + ColDefer: {"mode", "defer_days"},
		ColWindow:                {"window_start", "window_end"},
		ColPin:                   {"pin"},
	} {
		if err := st.SavePolicy(t.Context(), Settings{Mode: "auto", DeferDays: 2}, strings.Split(cols, ",")...); err != nil {
			t.Fatal(err)
		}
		for _, c := range want {
			if !strings.Contains(spy.sql, c+" = EXCLUDED."+c) {
				t.Errorf("%s: %q not updated in %s", cols, c, spy.sql)
			}
		}
		for _, c := range []string{"pin", "channel", "mode", "defer_days", "last_check_at", "decision"} {
			if strings.Contains(spy.sql, c+" =") && !contains(want, c) {
				t.Errorf("%s: %q must not be written: %s", cols, c, spy.sql)
			}
		}
	}
	if err := st.SavePolicy(t.Context(), Settings{}, "decision"); err == nil {
		t.Error("an unknown column must be refused")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
