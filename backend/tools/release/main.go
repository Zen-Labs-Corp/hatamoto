// Command release makes and checks signed Hatamoto releases. It is a dev and CI
// tool and is never linked into the hatamoto binary. Run from backend/:
//
//	go run ./tools/release keygen --kind release|manifest --out DIR
//	go run ./tools/release record --version v1.2.3 --kind patch --facts facts.json \
//	    --rollback-floor v1.2.0 --min-from v1.0.0 --notes-url URL --dir dist --out release.json
//	go run ./tools/release record --check release.json [--dir dist] [--allow-same]
//	go run ./tools/release record --amend release.json [--security] [--kind K] [--rollback-floor V] [--min-from V]
//	go run ./tools/release sign-record release.json (--key FILE | --agent [--agent-key ID]) [--allow-same]
//	go run ./tools/release channel add|ramp|halt|unhalt|yank CHAN VERSION [flags]
//	go run ./tools/release channel resign --channel CHAN --out DIR --key FILE
//	go run ./tools/release verify --chan stable --manifest channel.json
//	go run ./tools/release verify --chan edge --record release.json --sig release.json.sig --dir DIR [--version vX.Y.Z]
//	go run ./tools/release verify --chan stable --channel channel.json --record release.json \
//	    --sig release.json.sig --artifact FILE --platform linux-amd64
//
// Release keys are used from a file or an ssh-agent; the manifest key is only
// ever used by CI. Nothing here touches the network.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// env carries what tests replace: the clock, the trust sets and the agent
// connection. The zero value means time.Now, the embedded keys and
// SSH_AUTH_SOCK.
type env struct {
	Now               func() time.Time
	Release, Manifest packages.TrustSet
	AgentConn         func() (net.Conn, error)
}

func (e env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e env) options(channel string) release.Options {
	return release.Options{Channel: channel, Release: e.Release, Manifest: e.Manifest, Now: e.Now}
}

func (e env) releaseTrust() packages.TrustSet {
	if e.Release != nil {
		return e.Release
	}
	return release.ReleaseTrust()
}

func (e env) dialAgent() (net.Conn, error) {
	if e.AgentConn != nil {
		return e.AgentConn()
	}
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK is not set; start an ssh-agent or use --key")
	}
	return net.Dial("unix", sock)
}

const usage = `usage: release <command> [flags]
  keygen       --kind release|manifest --out DIR
  record       --version V --kind patch|minor|major --facts F --rollback-floor V --min-from V --notes-url U --dir D --out F
  record       --check F [--dir D] [--allow-same]
  record       --amend F [--security] [--kind K] [--rollback-floor V] [--min-from V] [--allow-same]
  sign-record  FILE (--key KEY | --agent [--agent-key ID]) [--out FILE] [--allow-same]
  channel      add|ramp|halt|unhalt|yank|resign (see channel with no arguments)
  verify       --chan stable|edge|canary (--manifest F | --channel F --record F --sig F --artifact F --platform P | --record F --sig F --dir D [--version V])`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr, env{}); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer, e env) error {
	if len(args) == 0 {
		return fmt.Errorf("a command is required\n%s", usage)
	}
	switch args[0] {
	case "keygen":
		return cmdKeygen(args[1:], stdout, stderr, e)
	case "record":
		return cmdRecord(args[1:], stdout, stderr, e)
	case "sign-record":
		return cmdSignRecord(args[1:], stdout, stderr, e)
	case "channel":
		return cmdChannel(args[1:], stdout, stderr, e)
	case "verify":
		return cmdVerify(args[1:], stdout, stderr, e)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", args[0], usage)
}

// parseFlags parses fs over args, letting positional arguments appear between
// flags (the flag package alone stops at the first one). It returns them.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}
