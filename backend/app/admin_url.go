package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hatamoto.dev/core/internal/adminpath"
)

const weakFlag = "--i-understand-weak"

// exitError asks Main to exit with code without logging anything: the
// command already told the operator what happened on stdout.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

type adminURLAction int

const (
	adminURLShow adminURLAction = iota
	adminURLSet
	adminURLRandom
)

type adminURLArgs struct {
	action adminURLAction
	path   string
	weak   bool
}

// parseAdminURLArgs reads the arguments after "admin-url":
//
//	(none) | set <path> [--i-understand-weak] | random
//
// The flag may come before or after the path.
func parseAdminURLArgs(args []string) (adminURLArgs, error) {
	var a adminURLArgs
	var pos []string
	for _, s := range args {
		switch {
		case s == weakFlag:
			a.weak = true
		case strings.HasPrefix(s, "--"):
			return a, fmt.Errorf("unknown flag %q", s)
		default:
			pos = append(pos, s)
		}
	}
	switch {
	case len(pos) == 0 && !a.weak:
		a.action = adminURLShow
	case len(pos) == 2 && pos[0] == "set":
		a.action, a.path = adminURLSet, pos[1]
	case len(pos) == 1 && pos[0] == "random" && !a.weak:
		a.action = adminURLRandom
	default:
		return a, fmt.Errorf("usage: admin-url | admin-url set <path> [%s] | admin-url random", weakFlag)
	}
	return a, nil
}

// runAdminURL shows or changes the stored admin address. args are the
// arguments after "admin-url". baseURL returns the public origin or "".
// A refusal is printed to out and returned as exitError{1}.
func runAdminURL(ctx context.Context, st adminStore, site adminpath.Site, args []string, env func(string) string, baseURL func(context.Context) string, out io.Writer) error {
	a, err := parseAdminURLArgs(args)
	if err != nil {
		return err
	}
	// With HATAMOTO_ADMIN_PATH set, the pin is what is served, so it is the
	// address to print; the note tells what is stored.
	pin := strings.TrimSpace(env("HATAMOTO_ADMIN_PATH"))
	if p, err := adminpath.Normalize(pin); err == nil {
		pin = p
	}
	link := func(path string) string {
		if pin != "" {
			path = pin
		}
		return strings.TrimRight(baseURL(ctx), "/") + path
	}
	pinNote := func(stored string) {
		if pin != "" {
			fmt.Fprintf(out, "note: HATAMOTO_ADMIN_PATH pins the served address to %s; the stored value is %s and is not what is served\n", pin, stored)
		}
	}

	switch a.action {
	case adminURLShow:
		cur, err := st.Get(ctx)
		if err != nil {
			return err
		}
		stored := "not chosen yet"
		if cur.Path != nil {
			stored = *cur.Path
		}
		switch {
		case pin != "":
			fmt.Fprintln(out, link(pin))
		case cur.Path == nil:
			fmt.Fprintln(out, "admin address is not chosen yet; hatamoto picks one at its next start")
		default:
			fmt.Fprintln(out, link(*cur.Path))
		}
		pinNote(stored)
		return nil

	case adminURLRandom:
		p, err := adminpath.Generate()
		if err != nil {
			return err
		}
		if err := st.Set(ctx, p, "cli"); err != nil {
			return err
		}
		fmt.Fprintf(out, "admin address set to %s (strong)\n%s\n", p, link(p))
		pinNote(p)
		fmt.Fprintln(out, "restart hatamoto to apply")
		return nil
	}

	res := adminpath.Assess(ctx, a.path, site)
	refuse := func(format string, v ...any) error {
		fmt.Fprintf(out, "refused: "+format+"\n", v...)
		return exitError{1}
	}
	why := reasonText(res.Reasons)
	switch res.Level {
	case adminpath.LevelInvalid:
		return refuse("%q is not a valid admin address (%s).", res.Path, why)
	case adminpath.LevelReserved:
		return refuse("%q is reserved (%s).", res.Path, why)
	case adminpath.LevelWeak:
		if !a.weak {
			// The first reason is the main one; the brief's reading shows only it.
			return refuse("%q is weak (%s). Re-run with %s to use it anyway.", res.Path, res.Reasons[0].Text, weakFlag)
		}
		if err := st.SetAck(ctx, res.Path, "cli"); err != nil {
			return err
		}
		cur, err := st.Get(ctx)
		if err != nil {
			return err
		}
		at := time.Now()
		if cur.AckAt != nil {
			at = *cur.AckAt
		}
		fmt.Fprintf(out, "admin address set to %s (weak, acknowledged by cli at %s)\n", res.Path, at.UTC().Format(time.RFC3339))
	default:
		if err := st.Set(ctx, res.Path, "cli"); err != nil {
			return err
		}
		fmt.Fprintf(out, "admin address set to %s (%s)\n", res.Path, res.Level)
	}
	fmt.Fprintln(out, link(res.Path))
	pinNote(res.Path)
	fmt.Fprintln(out, "restart hatamoto to apply")
	return nil
}

// adminBaseURL is the public origin for the printed link: PUBLIC_BASE_URL,
// then the site config's public_base_url, else "" (the path alone is printed).
func adminBaseURL(pool *pgxpool.Pool, env func(string) string) func(context.Context) string {
	return func(ctx context.Context) string {
		if v := strings.TrimSpace(env("PUBLIC_BASE_URL")); v != "" {
			return v
		}
		var v string
		err := pool.QueryRow(ctx, `SELECT public_base_url FROM hatamoto_site_config WHERE id = TRUE`).Scan(&v)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ""
		}
		return strings.TrimSpace(v)
	}
}
