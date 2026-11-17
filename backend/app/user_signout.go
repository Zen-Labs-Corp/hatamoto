package app

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/jsonx"
)

// signOutRefusal is printed when `user sign-out-all` runs without --yes.
const signOutRefusal = "error: signs out every user and revokes every API key; re-run with --yes"

// signOutCounts are the rows newly changed by one sign-out-all run.
type signOutCounts struct{ Sessions, APIKeys, Tokens, Previews int64 }

// parseSignOutFlags parses the arguments after `user sign-out-all`.
func parseSignOutFlags(args []string) (yes bool, err error) {
	fs := flag.NewFlagSet("user sign-out-all", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&yes, "yes", false, "confirm the sign-out")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if fs.NArg() > 0 {
		return false, fmt.Errorf("user sign-out-all: unexpected argument %q", fs.Arg(0))
	}
	return yes, nil
}

// signOutAll revokes every session, API key, unused invite/reset token and
// live preview link, and writes the audit row, in one transaction. hook, when
// not nil, runs after the revocations and before the audit insert; an error
// from it rolls everything back.
func signOutAll(ctx context.Context, db *database.DB, hook func() error) (signOutCounts, error) {
	ctx = database.WithSuperadmin(ctx)
	var c signOutCounts
	err := db.WithTx(ctx, func(tx pgx.Tx) error {
		steps := []struct {
			dst *int64
			sql string
		}{
			{&c.Sessions, `DELETE FROM hatamoto_sessions`},
			{&c.APIKeys, `UPDATE hatamoto_api_keys SET is_active = false, revoked_at = now(), updated_at = now() WHERE revoked_at IS NULL`},
			{&c.Tokens, `UPDATE hatamoto_user_tokens SET used_at = now() WHERE used_at IS NULL`},
			{&c.Previews, `UPDATE hatamoto_preview_links SET revoked_at = now() WHERE revoked_at IS NULL`},
		}
		for _, s := range steps {
			tag, err := tx.Exec(ctx, s.sql)
			if err != nil {
				return err
			}
			*s.dst = tag.RowsAffected()
		}
		if hook != nil {
			if err := hook(); err != nil {
				return err
			}
		}
		raw, err := jsonx.Marshal(map[string]any{
			"sessions":      c.Sessions,
			"api_keys":      c.APIKeys,
			"invite_reset":  c.Tokens,
			"preview_links": c.Previews,
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO hatamoto_admin_audit (actor_id, actor, action, target, detail) VALUES (NULL, 'cli', 'user.sign_out_all', 'site', $1::jsonb)`,
			raw)
		return err
	})
	return c, err
}

// renderSignOut prints the output contract: four "<label> <n> <verb>" lines
// (label padded to 13 columns) and a closing sentence.
func renderSignOut(out io.Writer, c signOutCounts) {
	fmt.Fprintf(out, "%-13s %d %s\n", "sessions", c.Sessions, "deleted")
	fmt.Fprintf(out, "%-13s %d %s\n", "api keys", c.APIKeys, "revoked")
	fmt.Fprintf(out, "%-13s %d %s\n", "invite/reset", c.Tokens, "revoked")
	fmt.Fprintf(out, "%-13s %d %s\n", "preview links", c.Previews, "revoked")
	fmt.Fprintln(out, "users, passwords and second factors are unchanged")
}

func runUserSignOutAll(ctx context.Context, db *database.DB, out io.Writer) error {
	c, err := signOutAll(ctx, db, nil)
	if err != nil {
		return fmt.Errorf("sign out all: %w", err)
	}
	renderSignOut(out, c)
	return nil
}
