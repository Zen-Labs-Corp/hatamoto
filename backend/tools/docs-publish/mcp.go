package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// mcpProtocol is the MCP revision this client speaks (the server supports it).
const mcpProtocol = "2025-06-18"

// MCP is a minimal Model Context Protocol client over streamable HTTP
// (POST /mcp with a bearer key). It does the initialize handshake lazily on
// the first call and keeps the Mcp-Session-Id the server returns.
type MCP struct {
	BaseURL string
	HTTP    *http.Client
	// Sleep replaces time.Sleep in tests.
	Sleep func(time.Duration)

	key     string
	session string
	nextID  int
	ready   bool
}

// NewMCP builds a client for cfg.
func NewMCP(cfg Config) *MCP {
	return &MCP{BaseURL: cfg.URL, HTTP: newHTTPClient(), key: cfg.APIKey}
}

// ToolError is a tool that ran and reported an error (isError in the result).
type ToolError struct{ Tool, Msg string }

func (e *ToolError) Error() string { return fmt.Sprintf("mcp tool %s: %s", e.Tool, e.Msg) }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int   `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call runs one tool and decodes its JSON text result into out (nil to ignore).
func (m *MCP) Call(ctx context.Context, tool string, args any, out any) error {
	if err := m.handshake(ctx); err != nil {
		return err
	}
	raw, err := m.request(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return err
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp tool %s: unreadable result: %w", tool, err)
	}
	var text string
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	if res.IsError {
		return &ToolError{Tool: tool, Msg: text}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("mcp tool %s: result is not JSON: %w", tool, err)
	}
	return nil
}

// Close ends the session (best effort).
func (m *MCP) Close(ctx context.Context) {
	if m.session == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.BaseURL+"/mcp", nil)
	if err != nil {
		return
	}
	m.headers(req)
	if resp, err := m.HTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

func (m *MCP) handshake(ctx context.Context) error {
	if m.ready {
		return nil
	}
	_, err := m.request(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocol,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "docs-publish", "version": "1"},
	})
	if err != nil {
		return err
	}
	if _, err := m.send(ctx, rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return err
	}
	m.ready = true
	return nil
}

func (m *MCP) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.nextID++
	id := m.nextID
	resp, err := m.send(ctx, rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp %s: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
	}
	return resp.Result, nil
}

func (m *MCP) headers(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+m.key)
	req.Header.Set("Accept", "application/json, text/event-stream")
	if m.session != "" {
		req.Header.Set("Mcp-Session-Id", m.session)
		req.Header.Set("MCP-Protocol-Version", mcpProtocol)
	}
}

// send posts one message and, for a request, reads its response from either
// a JSON body or a text/event-stream body.
func (m *MCP) send(ctx context.Context, msg rpcRequest) (rpcResponse, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return rpcResponse{}, err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.BaseURL+"/mcp", bytes.NewReader(payload))
		if err != nil {
			return rpcResponse{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		m.headers(req)
		resp, err := m.HTTP.Do(req)
		if err != nil {
			return rpcResponse{}, redactURL(err)
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if rerr != nil {
			return rpcResponse{}, rerr
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait := time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
				wait = time.Duration(s) * time.Second
			}
			m.sleep(wait)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return rpcResponse{}, apiError(resp.StatusCode, body)
		}
		if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
			m.session = sid
		}
		if msg.ID == nil {
			return rpcResponse{}, nil
		}
		return decodeRPC(resp.Header.Get("Content-Type"), body, *msg.ID)
	}
}

func (m *MCP) sleep(d time.Duration) {
	if m.Sleep != nil {
		m.Sleep(d)
		return
	}
	time.Sleep(d)
}

// decodeRPC finds the response with the given id in a JSON or SSE body.
func decodeRPC(contentType string, body []byte, id int) (rpcResponse, error) {
	if strings.HasPrefix(contentType, "text/event-stream") {
		sc := bufio.NewScanner(bytes.NewReader(body))
		sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data:")
			if !ok {
				continue
			}
			var r rpcResponse
			if json.Unmarshal([]byte(strings.TrimSpace(data)), &r) == nil && r.ID != nil && *r.ID == id {
				return r, nil
			}
		}
		return rpcResponse{}, errors.New("mcp: no response for the request in the event stream")
	}
	var r rpcResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return r, fmt.Errorf("mcp: unreadable response: %w", err)
	}
	return r, nil
}
