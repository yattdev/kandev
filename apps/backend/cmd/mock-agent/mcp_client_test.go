package main

import "testing"

// TestMCPCallOutput verifies that structured content and the error flag are
// included only when the tool actually returned them, so a script asserting
// on a read-only tool's structured payload (see
// provider-usage-packaged-plugin.spec.ts) can rely on the keys being present
// exactly when the tool declares an output schema or signals a failure.
func TestMCPCallOutput(t *testing.T) {
	t.Run("includes structuredContent when present", func(t *testing.T) {
		structured := map[string]any{"schema_version": "1"}
		got := mcpCallOutput("fallback text", structured, false)
		if got[toolKeyResult] != "fallback text" {
			t.Errorf("result = %v, want %q", got[toolKeyResult], "fallback text")
		}
		if got[toolKeyStructured] == nil {
			t.Fatal("expected structuredContent to be present")
		}
		gotStructured, ok := got[toolKeyStructured].(map[string]any)
		if !ok || gotStructured["schema_version"] != "1" {
			t.Errorf("structuredContent = %v, want %v", got[toolKeyStructured], structured)
		}
		if _, ok := got[toolKeyIsError]; ok {
			t.Errorf("isError = %v, want key absent", got[toolKeyIsError])
		}
	})

	t.Run("omits structuredContent when the tool returned none", func(t *testing.T) {
		got := mcpCallOutput("fallback text", nil, false)
		if got[toolKeyResult] != "fallback text" {
			t.Errorf("result = %v, want %q", got[toolKeyResult], "fallback text")
		}
		if _, ok := got[toolKeyStructured]; ok {
			t.Errorf("structuredContent = %v, want key absent", got[toolKeyStructured])
		}
	})

	t.Run("includes isError when the MCP result signals a tool-level failure", func(t *testing.T) {
		got := mcpCallOutput("rejected: bad arguments", nil, true)
		if got[toolKeyIsError] != true {
			t.Errorf("isError = %v, want true", got[toolKeyIsError])
		}
		if got[toolKeyResult] != "rejected: bad arguments" {
			t.Errorf("result = %v, want %q", got[toolKeyResult], "rejected: bad arguments")
		}
	})
}
