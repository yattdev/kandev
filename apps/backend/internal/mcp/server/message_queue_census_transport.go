package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
)

// The MCP library decodes application arguments before invoking a tool. This
// boundary preserves the original serialized byte count for the removal tool.
func queueArgumentBoundaryHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read MCP request", http.StatusBadRequest)
			return
		}
		body = refuseOversizeQueueArguments(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

func refuseOversizeQueueArguments(body []byte) []byte {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return body
	}
	var method string
	if json.Unmarshal(envelope["method"], &method) != nil || method != string(mcpprotocol.MethodToolsCall) {
		return body
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(envelope["params"], &params) != nil {
		return body
	}
	var name string
	if json.Unmarshal(params["name"], &name) != nil ||
		(name != removeClaimedQueueTool && name != "remove_message_queue_entry") ||
		len(params["arguments"]) <= 2048 {
		return body
	}
	params["arguments"] = json.RawMessage("null")
	encodedParams, err := json.Marshal(params)
	if err != nil {
		return body
	}
	envelope["params"] = encodedParams
	filtered, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return filtered
}
