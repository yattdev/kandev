package main

import "testing"

// TestMCPCallOutput verifies that structured content is included only when
// the tool actually returned it, so a script asserting on a read-only tool's
// structured payload (see provider-usage-packaged-plugin.spec.ts) can rely on
// the key being present exactly when the tool declares an output schema.
func TestMCPCallOutput(t *testing.T) {
	t.Run("includes structuredContent when present", func(t *testing.T) {
		structured := map[string]any{"schema_version": "1"}
		got := mcpCallOutput("fallback text", structured)
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
	})

	t.Run("omits structuredContent when the tool returned none", func(t *testing.T) {
		got := mcpCallOutput("fallback text", nil)
		if got[toolKeyResult] != "fallback text" {
			t.Errorf("result = %v, want %q", got[toolKeyResult], "fallback text")
		}
		if _, ok := got[toolKeyStructured]; ok {
			t.Errorf("structuredContent = %v, want key absent", got[toolKeyStructured])
		}
	})
}
