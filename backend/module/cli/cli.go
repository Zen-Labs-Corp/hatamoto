package cli

import "hatamoto.dev/core/internal/modhost"

// Env is what a command receives: the database pool, a logger, the data
// directory and the output streams.
type Env = modhost.CLIEnv

// Command is a command added to the hatamoto binary. Name is lower case
// ([a-z][a-z0-9-]{0,31}); Usage is the text printed after the name in help.
type Command = modhost.CLICommand

// Register adds a command. Call it from an init function: it panics after
// app.Main has started, on an invalid or duplicate name and on a nil Run.
// Core command names always win over a module command of the same name.
func Register(c Command) { modhost.RegisterCommand(c) }

// Exit makes the command's process exit with code. A nil err prints nothing.
func Exit(code int, err error) error { return modhost.ExitError{Code: code, Err: err} }
