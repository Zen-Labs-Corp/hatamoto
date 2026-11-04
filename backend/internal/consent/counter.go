package consent

import (
	"context"
	"fmt"
	"time"
)

// Counter counts the choices visitors made, per UTC day. Only day, choice and
// a total are stored.
type Counter struct{ db Querier }

// NewCounter returns a Counter over db (a *pgxpool.Pool fits).
func NewCounter(db Querier) *Counter { return &Counter{db: db} }

// DayCounts is one day's totals.
type DayCounts struct {
	Day               string // YYYY-MM-DD, UTC
	All, None, Custom int64
}

// ValidChoice reports whether c is one of all, none, custom.
func ValidChoice(c string) bool {
	return c == ChoiceAll || c == ChoiceNone || c == ChoiceCustom
}

// Inc adds one to the count of choice on the UTC day of t.
func (c *Counter) Inc(ctx context.Context, t time.Time, choice string) error {
	if !ValidChoice(choice) {
		return fmt.Errorf("consent: unknown choice %q", choice)
	}
	day := t.UTC().Format("2006-01-02")
	_, err := c.db.Exec(ctx, `
		INSERT INTO hatamoto_consent_daily (day, choice, n) VALUES ($1::date, $2, 1)
		ON CONFLICT (day, choice) DO UPDATE SET n = hatamoto_consent_daily.n + 1`,
		day, choice)
	if err != nil {
		return fmt.Errorf("count consent choice: %w", err)
	}
	return nil
}

// Last returns the totals of the last days UTC days ending today, newest
// first, one entry per day that has a count.
func (c *Counter) Last(ctx context.Context, days int) ([]DayCounts, error) {
	if days < 1 {
		days = 1
	}
	from := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := c.db.Query(ctx, `
		SELECT to_char(day, 'YYYY-MM-DD'), choice, n FROM hatamoto_consent_daily
		WHERE day >= $1::date ORDER BY day DESC`, from)
	if err != nil {
		return nil, fmt.Errorf("read consent counts: %w", err)
	}
	defer rows.Close()
	var out []DayCounts
	idx := map[string]int{}
	for rows.Next() {
		var day, choice string
		var n int64
		if err := rows.Scan(&day, &choice, &n); err != nil {
			return nil, fmt.Errorf("read consent counts: %w", err)
		}
		i, ok := idx[day]
		if !ok {
			i = len(out)
			idx[day] = i
			out = append(out, DayCounts{Day: day})
		}
		switch choice {
		case ChoiceAll:
			out[i].All = n
		case ChoiceNone:
			out[i].None = n
		case ChoiceCustom:
			out[i].Custom = n
		}
	}
	return out, rows.Err()
}
