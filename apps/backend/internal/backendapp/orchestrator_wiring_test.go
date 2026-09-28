package backendapp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestAgentFamilyResolverIsWired guards the one line that makes per-step
// configure_session model selection work in the real binary.
//
// Every orchestrator test installs a resolver straight into the Service struct,
// so deleting SetAgentFamilyResolver from provideOrchestrator leaves that whole
// suite green while the shipped backend silently falls back to comparing a
// hand-written agent family ("Claude") against the canonical ID a session stores
// ("claude-acp"). Nothing matches, no rule applies, and every workflow step runs
// on the task profile's model again — the defect this wiring exists to fix,
// reintroduced with no failing test.
func TestAgentFamilyResolverIsWired(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "orchestrator.go", nil, 0)
	if err != nil {
		t.Fatalf("parse orchestrator.go: %v", err)
	}

	var provideFn *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "provideOrchestrator" {
			provideFn = fn
			break
		}
	}
	if provideFn == nil {
		t.Fatal("provideOrchestrator not found in orchestrator.go; re-point this guard at the DI function")
	}

	var resolverArg string
	var calls int
	ast.Inspect(provideFn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "SetAgentFamilyResolver" || len(call.Args) != 1 {
			return true
		}
		calls++
		if ident, ok := call.Args[0].(*ast.Ident); ok {
			resolverArg = ident.Name
		}
		return true
	})

	if calls == 0 {
		t.Fatal("provideOrchestrator never calls SetAgentFamilyResolver; " +
			"configure_session rules cannot resolve an agent family without it, so per-step " +
			"model selection silently stops working")
	}
	if calls > 1 {
		t.Errorf("SetAgentFamilyResolver is called %d times in provideOrchestrator; expected exactly one wiring site", calls)
	}
	// The agent registry is the only collaborator that knows every installed
	// agent, including the custom TUI agents a user registers at runtime.
	if resolverArg != "agentRegistry" {
		t.Errorf("SetAgentFamilyResolver is wired with %q, want the agent registry (agentRegistry)", resolverArg)
	}
}

// TestProvideOrchestratorWiresExactTaskDecisionEvidence guards the only
// production composition site for the SQLite-only composite projection. The
// plugin service can start before the orchestrator, so omitting this wire
// leaves every exact decision-evidence request fail-closed as unavailable.
func TestProvideOrchestratorWiresExactTaskDecisionEvidence(t *testing.T) {
	provideFn := findFuncDecl(t, "orchestrator.go", "wireExactPluginEvidence")
	wired := false
	ast.Inspect(provideFn, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SetExactTaskDecisionEvidence" {
			return true
		}
		receiver, receiverOK := selector.X.(*ast.Ident)
		argument, argumentOK := call.Args[0].(*ast.Ident)
		wired = receiverOK && receiver.Name == "pluginsSvc" && argumentOK && argument.Name == "composite"
		return true
	})
	if !wired {
		t.Fatal("wireExactPluginEvidence does not wire the composite exact decision evidence reader into plugins")
	}
}

// TestProvideOrchestratorWiresExactTaskCommandGrantIssuer guards the private
// command-admission seam. The public Host writer remains unavailable until the
// complete contract is ready, but production composition must still attach the
// same queue authority to grant issuance as it does to command application.
func TestProvideOrchestratorWiresExactTaskCommandGrantIssuer(t *testing.T) {
	provideFn := findFuncDecl(t, "orchestrator.go", "wireExactPluginEvidence")
	wired := false
	ast.Inspect(provideFn, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SetExactTaskCommandGrantIssuer" {
			return true
		}
		receiver, receiverOK := selector.X.(*ast.Ident)
		wired = receiverOK && receiver.Name == "pluginsSvc"
		return true
	})
	if !wired {
		t.Fatal("wireExactPluginEvidence does not wire the exact task command grant issuer into plugins")
	}
}
