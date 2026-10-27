package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"hatamoto.dev/core/internal/database"
)

// Robots policy. Only "deny" is ever stored or emitted: an agent-specific
// robots.txt group replaces the "*" group for that agent, so an explicit
// "Allow" group would silently drop the site's default Disallow lines. An empty
// policy therefore means "robots.txt unchanged". The table is never seeded.

const (
	RobotsAllow = "allow"
	RobotsDeny  = "deny"
)

// allPurposes is the display order of the policy rows.
var allPurposes = []Purpose{
	PurposeTraining, PurposeSearchIndex, PurposeUserFetch, PurposeOtherAI,
	PurposeSearch, PurposeSocial, PurposeSEO, PurposeArchive,
}

// DenyTokens returns the distinct robots.txt user-agent tokens of every
// registry agent whose purpose is denied, sorted. Agents without a
// robots_token are skipped. Token-only entries (Google-Extended) count: they
// are exactly what robots.txt is for.
func DenyTokens(reg *Registry, denied []Purpose) []string {
	set := map[Purpose]bool{}
	for _, p := range denied {
		set[p] = true
	}
	seen := map[string]bool{}
	out := []string{}
	for _, a := range reg.Agents {
		if a.RobotsToken == "" || !set[a.Purpose] || seen[a.RobotsToken] {
			continue
		}
		seen[a.RobotsToken] = true
		out = append(out, a.RobotsToken)
	}
	slices.Sort(out)
	return out
}

// RobotsStore persists the denied purposes.
type RobotsStore interface {
	DeniedPurposes(ctx context.Context) ([]Purpose, error)
	// SetDenied replaces the whole policy: exactly these purposes are denied,
	// every other row is deleted.
	SetDenied(ctx context.Context, denied []Purpose) error
}

// RobotsRepository is the Postgres RobotsStore.
type RobotsRepository struct{ db *database.DB }

// NewRobotsRepository creates the Postgres-backed store.
func NewRobotsRepository(db *database.DB) *RobotsRepository { return &RobotsRepository{db: db} }

func (r *RobotsRepository) DeniedPurposes(ctx context.Context) ([]Purpose, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT purpose FROM hatamoto_agent_robots_policy WHERE action = 'deny' ORDER BY purpose`)
	if err != nil {
		return nil, fmt.Errorf("robots policy: %w", err)
	}
	defer rows.Close()
	out := []Purpose{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, Purpose(p))
	}
	return out, rows.Err()
}

func (r *RobotsRepository) SetDenied(ctx context.Context, denied []Purpose) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("get connection: %w", err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_agent_robots_policy`); err != nil {
		return fmt.Errorf("clear policy: %w", err)
	}
	if len(denied) > 0 {
		names := make([]string, len(denied))
		for i, p := range denied {
			names[i] = string(p)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO hatamoto_agent_robots_policy (purpose, action) SELECT unnest($1::text[]), 'deny'`, names); err != nil {
			return fmt.Errorf("save policy: %w", err)
		}
	}
	return tx.Commit(ctx)
}

type robotsPurposeRow struct {
	Purpose             string   `json:"purpose"`
	Action              string   `json:"action"`
	Tokens              []string `json:"tokens"`
	IgnoresRobotsAgents []string `json:"ignores_robots_agents"`
}

type robotsPolicyResponse struct {
	Purposes []robotsPurposeRow `json:"purposes"`
}

func (h *AdminHandlers) robotsResponse(denied []Purpose) robotsPolicyResponse {
	resp := robotsPolicyResponse{Purposes: make([]robotsPurposeRow, 0, len(allPurposes))}
	for _, p := range allPurposes {
		row := robotsPurposeRow{
			Purpose: string(p), Action: RobotsAllow,
			Tokens: []string{}, IgnoresRobotsAgents: []string{},
		}
		if slices.Contains(denied, p) {
			row.Action = RobotsDeny
		}
		row.Tokens = DenyTokens(h.reg, []Purpose{p})
		for _, a := range h.reg.Agents {
			if a.Purpose == p && a.IgnoresRobots {
				row.IgnoresRobotsAgents = append(row.IgnoresRobotsAgents, a.Name)
			}
		}
		slices.Sort(row.IgnoresRobotsAgents)
		resp.Purposes = append(resp.Purposes, row)
	}
	return resp
}

// HandleGetRobotsPolicy serves GET /robots-policy: one row per purpose with the
// stored action ("allow" when nothing is stored), the robots.txt tokens it
// controls and the agents known to ignore robots.txt.
func (h *AdminHandlers) HandleGetRobotsPolicy(w http.ResponseWriter, r *http.Request) {
	denied, err := h.store.DeniedPurposes(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load robots policy")
		return
	}
	writeAdminJSON(w, http.StatusOK, h.robotsResponse(denied))
}

const maxRobotsBody = 16 << 10

// HandlePutRobotsPolicy serves PUT /robots-policy with {"purposes":[{"purpose","action"}]}.
// It replaces the policy: purposes with action "deny" are stored, everything
// else (including purposes omitted from the body) is deleted. 400 on an
// unknown purpose, an action other than allow/deny, or a repeated purpose.
func (h *AdminHandlers) HandlePutRobotsPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Purposes []struct {
			Purpose string `json:"purpose"`
			Action  string `json:"action"`
		} `json:"purposes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRobotsBody)).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	seen := map[string]bool{}
	denied := []Purpose{}
	for _, row := range body.Purposes {
		if !validPurposes[Purpose(row.Purpose)] {
			writeAdminError(w, http.StatusBadRequest, "unknown purpose: "+row.Purpose)
			return
		}
		if seen[row.Purpose] {
			writeAdminError(w, http.StatusBadRequest, "duplicate purpose: "+row.Purpose)
			return
		}
		seen[row.Purpose] = true
		switch row.Action {
		case RobotsDeny:
			denied = append(denied, Purpose(row.Purpose))
		case RobotsAllow:
		default:
			writeAdminError(w, http.StatusBadRequest, "action must be allow or deny")
			return
		}
	}
	if err := h.store.SetDenied(r.Context(), denied); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to save robots policy")
		return
	}
	writeAdminJSON(w, http.StatusOK, h.robotsResponse(denied))
}
