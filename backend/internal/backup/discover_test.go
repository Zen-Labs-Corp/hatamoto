package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"hatamoto.dev/core/internal/pluginhost"
)

// fakeRows returns string columns row by row.
type fakeRows struct {
	rows [][]string
	i    int
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) TypeMap() *pgtype.Map                         { return nil }
func (r *fakeRows) Next() bool                                   { r.i++; return r.i <= len(r.rows) }
func (r *fakeRows) Scan(dest ...any) error {
	for i, d := range dest {
		*(d.(*string)) = r.rows[r.i-1][i]
	}
	return nil
}

// fakeQuerier answers the catalog query with schemas and the credentials query
// with names (or an error).
type fakeQuerier struct {
	schemas  [][]string
	names    []string
	namesErr error
}

func (q *fakeQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "hatamoto_plugin_credentials") {
		if q.namesErr != nil {
			return nil, q.namesErr
		}
		var rows [][]string
		for _, n := range q.names {
			rows = append(rows, []string{n})
		}
		return &fakeRows{rows: rows}, nil
	}
	return &fakeRows{rows: q.schemas}, nil
}

func (q *fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func TestDiscoverNamesFromCredentials(t *testing.T) {
	q := &fakeQuerier{
		schemas: [][]string{{"plugin_foo_bar", "hm_p_abcd1234_foo_bar"}, {"plugin_webhooks", "hm_p_abcd1234_webhooks"}},
		names:   []string{"foo-bar"},
	}
	got, err := discover(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "foo-bar" || got[0].Role != "hm_p_abcd1234_foo_bar" || got[1].Name != "webhooks" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Part() != "plugin_foo_bar.dump" {
		t.Fatalf("part = %s", got[0].Part())
	}
}

func TestDiscoverCredentialsMissingIsFine(t *testing.T) {
	q := &fakeQuerier{schemas: [][]string{{"plugin_a", "hm_p_1_a"}}, namesErr: errors.New("no table")}
	got, err := discover(t.Context(), q)
	if err != nil || len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDiscoverRejectsOddNames(t *testing.T) {
	for _, row := range [][]string{{"plugin_A", "hm_p_1_a"}, {"plugin_a", "Bad Role"}, {"plugin_a", ""}} {
		if _, err := discover(t.Context(), &fakeQuerier{schemas: [][]string{row}}); err == nil {
			t.Errorf("%v accepted", row)
		}
	}
}

func TestSchemaNameMatchesPluginhost(t *testing.T) {
	for _, n := range []string{"webhooks", "foo-bar", "a-b-c"} {
		if schemaName(n) != pluginhost.SchemaName(n) {
			t.Errorf("%s: %s != %s", n, schemaName(n), pluginhost.SchemaName(n))
		}
	}
}
