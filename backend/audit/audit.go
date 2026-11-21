package audit

import "hatamoto.dev/core/internal/auditstate"

// Event is one audit record. Detail never carries secrets.
type Event = auditstate.Event

// Sink receives the events core writes to hatamoto_admin_audit, after core has
// attempted its own table row (MCP tool calls are not included).
// tx is the business transaction when core has one (return an error to roll
// the change back) and nil otherwise (the change is committed; the error is
// logged). A sink must not commit or roll back tx.
type Sink = auditstate.Sink

// Actor kinds for Event.ActorKind.
const (
	KindUser   = "user"
	KindAPIKey = "api_key"
	KindPlugin = "plugin"
	KindSystem = "system"
	KindCLI    = "cli"
)

// Results for Event.Result. An empty result means ResultOK.
const (
	ResultOK     = "ok"
	ResultDenied = "denied"
	ResultError  = "error"
)

// Errors returned by Record. ErrNoTx: tx was nil. ErrSink wraps a registered
// sink's error (the caller must fail the change). ErrTable wraps a failed
// default table row (tx stays usable).
var (
	ErrNoTx  = auditstate.ErrNoTx
	ErrSink  = auditstate.ErrSink
	ErrTable = auditstate.ErrTable
)

// Register installs s. Call it from an init function. It panics after
// app.Main started, on a second call, or with a nil sink.
func Register(s Sink) { auditstate.Register(s) }
