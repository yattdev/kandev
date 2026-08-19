package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

var (
	mcpClients   = map[string]*mcpclient.Client{}
	mcpClientsMu sync.Mutex
)

// getMCPClient returns (or creates) an initialized MCP client for the named server.
func getMCPClient(serverName string) (*mcpclient.Client, error) {
	mcpClientsMu.Lock()
	defer mcpClientsMu.Unlock()

	if c, ok := mcpClients[serverName]; ok {
		return c, nil
	}

	srv, ok := mcpServers[serverName]
	if !ok {
		return nil, fmt.Errorf("unknown MCP server: %s", serverName)
	}

	c, err := mcpclient.NewSSEMCPClient(srv.URL)
	if err != nil {
		return nil, fmt.Errorf("create SSE client for %s: %w", serverName, err)
	}

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("start MCP client %s: %w", serverName, err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "mock-agent",
		Version: "1.0",
	}

	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize MCP client %s: %w", serverName, err)
	}

	// A real ACP client always lists tools before its first call, and the
	// server's plugin-tool registry is populated lazily on that first
	// tools/list (see internal/mcp/server's AddBeforeListTools hook) rather
	// than at server construction. Skipping this made a freshly launched
	// session's plugin agent tools invisible to every e2e script that went
	// straight to CallTool, with no way to exercise that catalog path at all.
	if _, err := c.ListTools(ctx, mcp.ListToolsRequest{}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("list tools for MCP client %s: %w", serverName, err)
	}

	mcpClients[serverName] = c
	return c, nil
}

// callMCPTool calls a tool on the named MCP server and returns the result text.
func callMCPTool(serverName, toolName string, args map[string]any) (string, error) {
	return callMCPToolCtx(context.Background(), serverName, toolName, args)
}

// callMCPToolCtx calls a tool on the named MCP server with a caller-provided context.
// Use this when the caller needs to impose a timeout on the MCP call.
func callMCPToolCtx(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	c, err := getMCPClient(serverName)
	if err != nil {
		return "", err
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args

	result, err := c.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("call tool %s/%s: %w", serverName, toolName, err)
	}

	return extractMCPResultText(result), nil
}

// callMCPToolFull calls a tool on the named MCP server and returns its
// extracted fallback text, any structured content, and whether the MCP
// result itself is a tool-level error (per the MCP spec, most tool failures
// — including a plugin's own argument/schema rejection — come back as a
// normal CallToolResult with IsError:true and a text explanation, not as a
// transport-level error from CallTool).
func callMCPToolFull(
	ctx context.Context, serverName, toolName string, args map[string]any,
) (text string, structured any, isError bool, err error) {
	c, err := getMCPClient(serverName)
	if err != nil {
		return "", nil, false, err
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args

	result, err := c.CallTool(ctx, req)
	if err != nil {
		return "", nil, false, fmt.Errorf("call tool %s/%s: %w", serverName, toolName, err)
	}

	return extractMCPResultText(result), result.StructuredContent, result.IsError, nil
}

// extractMCPResultText extracts text from an MCP CallToolResult.
func extractMCPResultText(result *mcp.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// registerACPMcpServers adds SSE MCP servers from an ACP NewSessionRequest
// to the global mcpServers map so callMCPTool can reach them.
func registerACPMcpServers(servers []acp.McpServer) {
	for _, s := range servers {
		if s.Sse != nil && s.Sse.Name != "" && s.Sse.Url != "" {
			if mcpServers == nil {
				mcpServers = make(map[string]mcpServerDef)
			}
			mcpServers[s.Sse.Name] = mcpServerDef{URL: s.Sse.Url, Type: "sse"}
			_, _ = fmt.Fprintf(logOutput, "mock-agent: registered ACP MCP server %s at %s\n", s.Sse.Name, s.Sse.Url)
		}
	}
}

// closeMCPClients closes all open MCP clients (called on shutdown).
func closeMCPClients() {
	mcpClientsMu.Lock()
	defer mcpClientsMu.Unlock()
	for _, c := range mcpClients {
		_ = c.Close()
	}
}
