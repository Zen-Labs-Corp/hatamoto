// Package commentsubmit runs the public comment submit pipeline (spec section 3).
//
// Its state (rate limit buckets, slow mode marks, the surge window) lives in
// memory and is per process.
package commentsubmit
