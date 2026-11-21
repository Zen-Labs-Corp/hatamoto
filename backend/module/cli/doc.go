// Package cli lets a wrapper module add a command to the hatamoto binary.
//
//	package main
//
//	import (
//		"context"
//
//		"hatamoto.dev/core/app"
//		"hatamoto.dev/core/module/cli"
//	)
//
//	func init() {
//		cli.Register(cli.Command{
//			Name:  "hello",
//			Usage: "print a greeting",
//			Run: func(ctx context.Context, env *cli.Env, args []string) error {
//				_, err := env.Out.Write([]byte("hello\n"))
//				return err
//			},
//		})
//	}
//
//	func main() { app.Main() }
//
// The command receives an open database pool, a logger, the data directory
// and the output streams. Return cli.Exit(code, err) to choose the exit
// code. Registering nothing leaves the binary unchanged. Register must run
// from an init function; later calls panic.
package cli
