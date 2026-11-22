package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"hatamoto.dev/core/internal/cache"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/llmstxt"
	"hatamoto.dev/core/internal/seo"
)

const (
	llmsContentType = "text/plain; charset=utf-8"
	// llmsCacheControl is the edge window; the server cache is dropped on every
	// change, so this is the only staleness.
	llmsCacheControl = "public, max-age=300, s-maxage=300"
	llmsCacheTTL     = time.Hour
)

// llmsResult is one finished build. base lets a caller that joined another
// host's build notice and build its own.
type llmsResult struct {
	base string
	body []byte
	busy bool // a bounded acquire gave up: the body may lack data, never cache it
}

// handleLLMsTxt serves /llms.txt, the plain-words site summary.
func (s *Server) handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	s.serveLLMs(w, r, false)
}

// handleLLMsFullTxt serves /llms-full.txt, every listed page as Markdown under
// a size cap.
func (s *Server) handleLLMsFullTxt(w http.ResponseWriter, r *http.Request) {
	s.serveLLMs(w, r, true)
}

func (s *Server) aiFilesSettings(ctx context.Context) seo.AIFiles {
	if set := s.seoSettings(ctx); set != nil {
		return set.AIFiles
	}
	return seo.AIFiles{}
}

func (s *Server) serveLLMs(w http.ResponseWriter, r *http.Request, full bool) {
	s.serveLLMsWith(w, r, full, func(ctx context.Context, base string, set seo.AIFiles) (llmsResult, error) {
		return s.buildLLMsFile(ctx, base, set, full)
	})
}

// serveLLMsWith is serveLLMs with the build step given (tests fake it).
func (s *Server) serveLLMsWith(w http.ResponseWriter, r *http.Request, full bool, buildFile func(ctx context.Context, base string, set seo.AIFiles) (llmsResult, error)) {
	ctx := r.Context()
	set := s.aiFilesSettings(ctx)
	if (full && set.FullOff) || (!full && set.LLMsOff) {
		http.NotFound(w, r)
		return
	}
	base := s.publicBaseURL(r)
	// Under the render: prefix, so every content or settings change drops it.
	key := cache.RenderedPageKey("llms:"+base, r.URL.Path)
	var gen uint64
	if s.infra != nil && s.infra.Cache != nil {
		gen = s.infra.Cache.Generation() // before the read: a build that raced a purge is not stored
		if body, err := s.infra.Cache.Get(ctx, key); err == nil && body != nil {
			if s.compressOn.Load() {
				if enc, packed := cachedVariant(r, s.infra.Cache, key, body, llmsCacheTTL, gen, s.logger.Warn); packed != nil {
					writeLLMsEncoded(w, r, enc, packed)
					return
				}
			}
			writeLLMs(w, r, body)
			return
		}
	}

	flight := &s.llmsFlight
	if full {
		flight = &s.llmsFullFlight
	}
	build := func(ctx context.Context) (llmsResult, error) { return buildFile(ctx, base, set) }
	res, err := flight.Do(ctx, build)
	if err == nil && res.base != base {
		res, err = build(ctx) // joined a build made for another host
	}
	if err != nil {
		name := "llms.txt"
		if full {
			name = "llms-full.txt"
		}
		s.logger.Error("failed to build "+name, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to build "+name)
		return
	}
	if !res.busy && !database.PoolBusyHit(ctx) && s.infra != nil && s.infra.Cache != nil {
		_, _ = s.infra.Cache.SetIfGeneration(ctx, key, res.body, llmsCacheTTL, gen)
	}
	writeLLMs(w, r, res.body)
}

// buildLLMsFile builds one file and records its state (hash, size, and for the
// index the snapshot diff). The state is skipped when the build may be partial.
func (s *Server) buildLLMsFile(ctx context.Context, base string, set seo.AIFiles, full bool) (llmsResult, error) {
	res := llmsResult{base: base}
	if full {
		body, stats, err := s.buildLLMsFull(ctx, base, set)
		if err != nil {
			return res, err
		}
		res.body = body
		res.busy = database.PoolBusyHit(ctx)
		if !res.busy && s.aiFilesState != nil {
			if err := s.aiFilesState.RecordFull(ctx, llmsHash(body), stats.Bytes, time.Now().UTC()); err != nil {
				s.logger.Warn("llms-full: record state", "error", err)
			}
		}
		return res, nil
	}
	body, m, err := s.buildLLMsIndex(ctx, base, set)
	if err != nil {
		return res, err
	}
	res.body = body
	res.busy = database.PoolBusyHit(ctx)
	if !res.busy && s.aiFilesState != nil {
		snap := llmstxt.SnapshotOf(m.site, m.sections)
		if err := s.aiFilesState.RecordIndex(ctx, snap, llmsHash(body), len(body), m.excluded, time.Now().UTC()); err != nil {
			s.logger.Warn("llms: record state", "error", err)
		}
	}
	return res, nil
}

func llmsHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func writeLLMs(w http.ResponseWriter, r *http.Request, body []byte) {
	h := w.Header()
	h.Set("Content-Type", llmsContentType)
	h.Set("Cache-Control", llmsCacheControl)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// writeLLMsEncoded serves the stored compressed form of a cached file.
func writeLLMsEncoded(w http.ResponseWriter, r *http.Request, enc httpx.Encoding, packed []byte) {
	h := w.Header()
	h.Set("Content-Encoding", string(enc))
	h.Add("Vary", "Accept-Encoding")
	h.Set("Content-Length", strconv.Itoa(len(packed)))
	h.Set("Content-Type", llmsContentType)
	h.Set("Cache-Control", llmsCacheControl)
	if r.Method != http.MethodHead {
		_, _ = w.Write(packed)
	}
}
