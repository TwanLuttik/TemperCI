package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Server is a stateless Streamable HTTP MCP handler.
type Server struct {
	fleet   Fleet
	version string
}

// New returns an MCP server over fleet. version is advertised in initialize.
func New(fleet Fleet, version string) *Server {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	return &Server{fleet: fleet, version: version}
}

// Handler serves POST /mcp JSON-RPC. GET is 405 (no SSE stream in v1).
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handlePOST(w, r)
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handlePOST(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Method == "" {
		writeRPC(w, r, nil, nil, &rpcError{Code: -32700, Message: "parse error"})
		return
	}
	notification := len(req.ID) == 0 || string(req.ID) == "null"
	result, rpcErr := s.dispatch(req.Method, req.Params)
	if notification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if rpcErr != nil {
		writeRPC(w, r, req.ID, nil, rpcErr)
		return
	}
	writeRPC(w, r, req.ID, result, nil)
}

func (s *Server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.initialize(), nil
	case "notifications/initialized", "notifications/cancelled":
		return map[string]any{}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		return s.callTool(params), nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) initialize() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    ServerName,
			"version": s.version,
		},
		"instructions": "Read-only TemperCI fleet. Start with fleet_overview, then list_jobs or get_job for failures. Logs are truncated to the last 8KiB per stream.",
	}
}

func writeRPC(w http.ResponseWriter, r *http.Request, id json.RawMessage, result any, rpcErr *rpcError) {
	env := map[string]any{"jsonrpc": "2.0"}
	if len(id) > 0 && string(id) != "null" {
		var decoded any
		if err := json.Unmarshal(id, &decoded); err == nil {
			env["id"] = decoded
		} else {
			env["id"] = nil
		}
	} else {
		env["id"] = nil
	}
	if rpcErr != nil {
		env["error"] = rpcErr
	} else {
		if result == nil {
			result = map[string]any{}
		}
		env["result"] = result
	}
	raw, err := json.Marshal(env)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	accept := r.Header.Get("Accept")
	wantJSON := strings.Contains(accept, "application/json") || accept == "" || accept == "*/*"
	wantSSE := strings.Contains(accept, "text/event-stream")
	if wantSSE && !wantJSON {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message\ndata: "))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte("\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
	_, _ = w.Write([]byte("\n"))
}
