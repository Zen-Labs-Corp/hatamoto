package analytics

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const trafficMigration = "../../migrations/202610311200_traffic_daily.sql"

var (
	createTableRe = regexp.MustCompile(`(?is)CREATE TABLE IF NOT EXISTS public\.(\w+)\s*\((.*?)\n\);`)
	columnRe      = regexp.MustCompile(`(?m)^\s+(\w+)\s+(?:date|text|bigint|boolean)\b`)
	// Whole underscore-separated tokens, so "visits" is not an "ip" hit.
	forbiddenTokenRe = regexp.MustCompile(`^(ip|ua|user|agent|referrer|referer|visitor|session|cookie)$`)
)

func trafficColumns(t *testing.T) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(trafficMigration)
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(string(b), "-- +goose Down", 2)[0]
	out := map[string][]string{}
	for _, m := range createTableRe.FindAllStringSubmatch(up, -1) {
		for _, c := range columnRe.FindAllStringSubmatch(m[2], -1) {
			out[m[1]] = append(out[m[1]], c[1])
		}
		sort.Strings(out[m[1]])
	}
	return out
}

// The traffic rollup holds aggregate counters only: no identifier of a visitor,
// a request or a referrer may ever become a column.
func TestTrafficMigrationHasNoPersonalColumns(t *testing.T) {
	tables := trafficColumns(t)
	want := map[string][]string{
		"hatamoto_traffic_daily":      {"day", "dim", "engaged", "form_seen", "form_started", "k1", "k2", "multi", "pageviews", "scrolled", "visits"},
		"hatamoto_analytics_settings": {"engagement_enabled", "id"},
	}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("columns = %v, want %v", tables, want)
	}
	for table, cols := range tables {
		for _, c := range cols {
			for _, tok := range strings.Split(c, "_") {
				if forbiddenTokenRe.MatchString(tok) {
					t.Errorf("%s.%s looks like personal data", table, c)
				}
			}
		}
	}
}
