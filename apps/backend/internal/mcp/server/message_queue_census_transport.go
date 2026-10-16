package mcp

import "net/http"

// The MCP decoder preserves the original arguments bytes for tool validation.
// Passing the request through avoids buffering unrelated MCP bodies twice.
func queueArgumentBoundaryHandler(next http.Handler) http.Handler {
	return next
}
