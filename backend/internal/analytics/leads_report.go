package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ChannelUnknown is the channel of a submission stored without attribution
// (NULL columns): it still counts as a lead.
const ChannelUnknown = "unknown"

// TouchKind selects which touch of a submission the lead reports use.
type TouchKind string

// The two touches stored on a submission.
const (
	TouchFirst TouchKind = "first"
	TouchLast  TouchKind = "last"
)

// ParseTouch reads a ?touch= value; anything but "last" is the first touch.
func ParseTouch(s string) TouchKind {
	if s == string(TouchLast) {
		return TouchLast
	}
	return TouchFirst
}

// LeadDay is the leads of one UTC day and channel.
type LeadDay struct {
	Day     string // 2006-01-02
	Channel string
	Leads   int64
}

// LeadRow is the leads of one key of a dimension. K2 is the channel of a
// source and the medium of a campaign, like TrafficTop.
type LeadRow struct {
	K1, K2 string
	Leads  int64
}

// LeadForm is the leads of one form.
type LeadForm struct {
	FormID uuid.UUID
	Name   string
	Leads  int64
}

// Lead is one recent submission with its attribution. Email is the
// submitter's address; the admin layer decides whether to show it.
type Lead struct {
	ID, FormID uuid.UUID
	FormName   string
	Email      string
	At         time.Time
	First      *Touch
	Last       *Touch
}

// A lead is any stored submission that is not spam (every form counts), read
// at query time; days are UTC. Columns come from the whitelist below, never
// from input.
const leadWhere = `s.status <> 'spam' AND (s.created_at AT TIME ZONE 'UTC')::date BETWEEN $1 AND $2`

func leadColumn(touch TouchKind, field string) string {
	if touch != TouchLast {
		touch = TouchFirst
	}
	switch field {
	case "channel", "source", "medium", "campaign", "landing":
		return string(touch) + "_" + field
	}
	panic("analytics: unknown lead column " + field)
}

// LeadsDaily returns the leads per UTC day and channel from..to, oldest day
// first. A submission without attribution is channel "unknown".
func (r *Reader) LeadsDaily(ctx context.Context, touch TouchKind, from, to time.Time) ([]LeadDay, error) {
	ch := leadColumn(touch, "channel")
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT (s.created_at AT TIME ZONE 'UTC')::date AS day, COALESCE(s.`+ch+`, $3), count(*)::bigint
		FROM hatamoto_form_submissions s WHERE `+leadWhere+`
		GROUP BY 1, 2 ORDER BY 1, 2`, from, to, ChannelUnknown)
	if err != nil {
		return nil, fmt.Errorf("leads daily: %w", err)
	}
	defer rows.Close()
	out := []LeadDay{}
	for rows.Next() {
		var (
			d  time.Time
			ld LeadDay
		)
		if err := rows.Scan(&d, &ld.Channel, &ld.Leads); err != nil {
			return nil, err
		}
		ld.Day = d.Format("2006-01-02")
		out = append(out, ld)
	}
	return out, rows.Err()
}

// LeadsTop returns the leads of one dimension (channel, source, campaign or
// landing) summed over from..to, most leads first. Channel counts NULL
// attribution as "unknown"; the other dimensions skip submissions that have no
// value for that key. limit <= 0 means no limit.
func (r *Reader) LeadsTop(ctx context.Context, dim Dim, touch TouchKind, from, to time.Time, limit int) ([]LeadRow, error) {
	var k1, k2, where string
	switch dim {
	case DimChannel:
		k1, k2 = "COALESCE(s."+leadColumn(touch, "channel")+", $3)", "''"
	case DimSource:
		k1, k2 = "s."+leadColumn(touch, "source"), "s."+leadColumn(touch, "channel")
		where = " AND s." + leadColumn(touch, "source") + " IS NOT NULL"
	case DimCampaign:
		k1, k2 = "s."+leadColumn(touch, "campaign"), "COALESCE(s."+leadColumn(touch, "medium")+", '')"
		where = " AND COALESCE(s." + leadColumn(touch, "campaign") + ", '') <> ''"
	case DimLanding:
		k1, k2 = "s."+leadColumn(touch, "landing"), "''"
		where = " AND COALESCE(s." + leadColumn(touch, "landing") + ", '') <> ''"
	default:
		return nil, fmt.Errorf("leads top: unsupported dimension %q", dim)
	}
	if limit <= 0 {
		limit = 1 << 30
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT `+k1+`, COALESCE(`+k2+`, ''), count(*)::bigint
		FROM hatamoto_form_submissions s WHERE `+leadWhere+where+` AND $3::text IS NOT NULL -- $3 is unused outside channel; keep the args fixed
		GROUP BY 1, 2 ORDER BY 3 DESC, 1, 2 LIMIT $4`, from, to, ChannelUnknown, limit)
	if err != nil {
		return nil, fmt.Errorf("leads top: %w", err)
	}
	defer rows.Close()
	out := []LeadRow{}
	for rows.Next() {
		var lr LeadRow
		if err := rows.Scan(&lr.K1, &lr.K2, &lr.Leads); err != nil {
			return nil, err
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}

// LeadsTotals returns the leads of from..to and how many of them have no
// attribution (stored before it was recorded, or posted without it).
func (r *Reader) LeadsTotals(ctx context.Context, touch TouchKind, from, to time.Time) (total, unknown int64, err error) {
	ch := leadColumn(touch, "channel")
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	err = conn.QueryRow(ctx, `SELECT count(*)::bigint, (count(*) FILTER (WHERE s.`+ch+` IS NULL))::bigint
		FROM hatamoto_form_submissions s WHERE `+leadWhere, from, to).Scan(&total, &unknown)
	if err != nil {
		return 0, 0, fmt.Errorf("leads totals: %w", err)
	}
	return total, unknown, nil
}

// LeadsByForm returns the leads of from..to per form, most leads first.
// limit <= 0 means no limit.
func (r *Reader) LeadsByForm(ctx context.Context, from, to time.Time, limit int) ([]LeadForm, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT f.id, f.name, count(*)::bigint
		FROM hatamoto_form_submissions s JOIN hatamoto_forms f ON f.id = s.form_id
		WHERE `+leadWhere+`
		GROUP BY f.id, f.name ORDER BY 3 DESC, f.name, f.id LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("leads by form: %w", err)
	}
	defer rows.Close()
	out := []LeadForm{}
	for rows.Next() {
		var lf LeadForm
		if err := rows.Scan(&lf.FormID, &lf.Name, &lf.Leads); err != nil {
			return nil, err
		}
		out = append(out, lf)
	}
	return out, rows.Err()
}

// RecentLeads returns the newest leads of from..to with the submitter's email:
// the form's first email-type field, else a key named "email" (empty when
// there is none). Only the admin page and CSV call it, behind the
// submissions permission; the MCP layer never does.
func (r *Reader) RecentLeads(ctx context.Context, from, to time.Time, limit int) ([]Lead, error) {
	if limit <= 0 {
		limit = 1 << 30
	}
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT s.id, s.form_id, f.name, s.created_at,
			COALESCE((SELECT s.data ->> (fld ->> 'name')
				FROM jsonb_array_elements(CASE WHEN jsonb_typeof(f.fields) = 'array' THEN f.fields ELSE '[]'::jsonb END) fld
				WHERE fld ->> 'type' = 'email' AND jsonb_typeof(s.data -> (fld ->> 'name')) = 'string'
				LIMIT 1), s.data ->> 'email', ''),
			s.first_channel, s.first_source, s.first_medium, s.first_campaign, s.first_landing,
			s.last_channel, s.last_source, s.last_medium, s.last_campaign, s.last_landing
		FROM hatamoto_form_submissions s JOIN hatamoto_forms f ON f.id = s.form_id
		WHERE `+leadWhere+`
		ORDER BY s.created_at DESC, s.id LIMIT $3`, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("recent leads: %w", err)
	}
	defer rows.Close()
	out := []Lead{}
	for rows.Next() {
		var (
			l     Lead
			first [5]*string
			last  [5]*string
		)
		if err := rows.Scan(&l.ID, &l.FormID, &l.FormName, &l.At, &l.Email,
			&first[0], &first[1], &first[2], &first[3], &first[4],
			&last[0], &last[1], &last[2], &last[3], &last[4]); err != nil {
			return nil, err
		}
		l.First, l.Last = touchOf(first), touchOf(last)
		out = append(out, l)
	}
	return out, rows.Err()
}

// touchOf builds a Touch from channel, source, medium, campaign, landing
// columns; nil when all are NULL or empty.
func touchOf(c [5]*string) *Touch {
	v := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	t := Touch{Channel: v(c[0]), Source: v(c[1]), Medium: v(c[2]), Campaign: v(c[3]), Landing: v(c[4])}
	if t == (Touch{}) {
		return nil
	}
	return &t
}
