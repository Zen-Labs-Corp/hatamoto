package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/session"
	"hatamoto.dev/core/internal/twofactor"
	"hatamoto.dev/core/internal/user"
)

const minPasswordLen = 12

// userFlags are shared by `user create` and `user reset-password`.
type userFlags struct {
	email         string
	name          string
	role          string
	password      string
	passwordStdin bool
}

func parseUserFlags(name string, args []string, create bool) (*userFlags, error) {
	f := &userFlags{}
	fs := flag.NewFlagSet("user "+name, flag.ContinueOnError)
	fs.StringVar(&f.email, "email", "", "user email (required)")
	if name != "reset-2fa" { // reset-2fa takes no password
		fs.StringVar(&f.password, "password", "", "password (visible in the process list; prefer --password-stdin)")
		fs.BoolVar(&f.passwordStdin, "password-stdin", false, "read the password from stdin")
	}
	if create {
		fs.StringVar(&f.name, "name", "", "display name")
		fs.StringVar(&f.role, "role", string(user.RoleOwner), "owner or editor")
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	f.email = strings.TrimSpace(f.email)
	if f.email == "" {
		return nil, errors.New("--email is required")
	}
	if f.password != "" && f.passwordStdin {
		return nil, errors.New("use either --password or --password-stdin")
	}
	return f, nil
}

// resolvePassword returns the password to set and whether it was generated.
func resolvePassword(f *userFlags, stdin io.Reader) (pw string, generated bool, err error) {
	switch {
	case f.passwordStdin:
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", false, fmt.Errorf("reading password from stdin: %w", err)
		}
		pw = strings.TrimRight(line, "\r\n")
	case f.password != "":
		pw = f.password
	default:
		pw, err = auth.GenerateRandomString(24)
		return pw, true, err
	}
	if len(pw) < minPasswordLen {
		return "", false, fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	return pw, false, nil
}

// runUserCreate creates a user. Without a password it generates one and prints it.
func runUserCreate(ctx context.Context, db *database.DB, f *userFlags, stdin io.Reader, out io.Writer) error {
	role, err := user.ParseRole(f.role)
	if err != nil {
		return err
	}
	pw, generated, err := resolvePassword(f, stdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	u, err := user.NewPostgresRepository(db).Create(ctx, &user.CreateUserInput{
		Email: f.email, Password: pw, Role: role, Name: f.name,
	}, hash)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "created %s user %s\n", u.Role, u.Email)
	if generated {
		fmt.Fprintf(out, "temporary password: %s\n", pw)
	}
	return nil
}

// runUserResetPassword sets a new password (generated and printed when none is
// given), clears the login lockout and signs the user out everywhere.
func runUserResetPassword(ctx context.Context, db *database.DB, f *userFlags, stdin io.Reader, out io.Writer) error {
	repo := user.NewPostgresRepository(db)
	u, err := repo.GetByEmail(ctx, f.email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return fmt.Errorf("no user with email %s", f.email)
		}
		return err
	}
	pw, generated, err := resolvePassword(f, stdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := repo.SetPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	if err := session.NewStore(db).RevokeAllForUser(ctx, u.ID); err != nil {
		return fmt.Errorf("password changed but revoking sessions failed: %w", err)
	}
	fmt.Fprintf(out, "password reset for %s\n", u.Email)
	if generated {
		fmt.Fprintf(out, "temporary password: %s\n", pw)
	}
	return nil
}

// runUserReset2FA removes every second factor of a user (authenticator app,
// passkeys, recovery codes) so they can sign in with the password again. It
// leaves sessions and the password alone and writes an audit row.
func runUserReset2FA(ctx context.Context, db *database.DB, f *userFlags, out io.Writer) error {
	u, err := user.NewPostgresRepository(db).GetByEmail(ctx, f.email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return fmt.Errorf("no user with email %s", f.email)
		}
		return err
	}
	rm, err := twofactor.NewStore(db.Pool()).Reset(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("removing second factors: %w", err)
	}
	detail := fmt.Sprintf(`{"totp":%t,"passkeys":%d,"recovery_codes":%d}`, rm.TOTP, rm.Passkeys, rm.RecoveryCodes)
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_admin_audit (actor_id, actor, action, target, detail) VALUES (NULL, 'cli', 'user.2fa.reset', $1, $2::jsonb)`, u.Email, detail); err != nil {
		return fmt.Errorf("second factors removed but the audit row failed: %w", err)
	}
	fmt.Fprintln(out, describeReset2FA(rm, u.Email))
	if rm.TOTP || rm.Passkeys > 0 || rm.RecoveryCodes > 0 {
		fmt.Fprintln(out, "They can sign in with their password and set up two-step sign-in again. Sessions and the password are unchanged.")
	}
	return nil
}

func describeReset2FA(rm twofactor.Removed, email string) string {
	var parts []string
	if rm.TOTP {
		parts = append(parts, "the authenticator app")
	}
	switch rm.Passkeys {
	case 0:
	case 1:
		parts = append(parts, "1 passkey")
	default:
		parts = append(parts, strconv.Itoa(rm.Passkeys)+" passkeys")
	}
	if rm.RecoveryCodes > 0 {
		parts = append(parts, "the recovery codes")
	}
	switch len(parts) {
	case 0:
		return email + " had no second factor to remove."
	case 1:
		return "Removed " + parts[0] + " for " + email + "."
	}
	return "Removed " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1] + " for " + email + "."
}
