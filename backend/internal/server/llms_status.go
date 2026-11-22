package server

import (
	"context"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/llmstxt"
)

// aiFilesRecent is how many recent changes the admin card is given.
const aiFilesRecent = 5

// aiFilesStatus is the admin "Files for AI tools" card's view of the stored
// state. It only reads the state row: it never builds a file, so opening the
// page costs no build.
func (s *Server) aiFilesStatus(ctx context.Context) (admin.AIFilesStatus, error) {
	if s.aiFilesState == nil {
		return admin.AIFilesStatus{}, nil
	}
	st, err := s.aiFilesState.Get(ctx)
	if err != nil {
		return admin.AIFilesStatus{}, err
	}
	return aiFilesStatusOf(st), nil
}

func aiFilesStatusOf(st llmstxt.State) admin.AIFilesStatus {
	out := admin.AIFilesStatus{
		LLMs: admin.AIFileStatus{ChangedAt: st.LLMsChangedAt, Bytes: st.LLMsBytes},
		Full: admin.AIFileStatus{ChangedAt: st.FullChangedAt, Bytes: st.FullBytes},
	}
	for _, c := range st.Changes[:min(aiFilesRecent, len(st.Changes))] {
		out.Changes = append(out.Changes, admin.AIFileChange{At: c.At, Text: c.Text})
	}
	return out
}
