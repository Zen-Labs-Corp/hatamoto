//go:build !hatamoto_qa

package server

import "net/http"

// qaSandboxForced is false: the browser acceptance seam exists only in
// builds tagged hatamoto_qa (see qa_on.go).
func qaSandboxForced() bool { return false }

// qaWrap returns h unchanged: the QA fixture routes exist only in tagged builds.
func (s *Server) qaWrap(h http.Handler) http.Handler { return h }
