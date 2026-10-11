package taskinstructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @covers AC-1, AC-4, AC-6
func TestManagedControlPathIsOutsideEveryCheckout(t *testing.T) {
	tasksBase := t.TempDir()
	taskRoot := filepath.Join(tasksBase, "task-42")
	checkout := filepath.Join(taskRoot, "repository")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := ManagedControlPath(tasksBase, "task-42", []string{checkout})
	if err != nil {
		t.Fatalf("ManagedControlPath() error = %v", err)
	}
	want := filepath.Join(taskRoot, ".kandev", "task-instructions", "AGENTS.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	inside, err := isWithin(checkout, path)
	if err != nil {
		t.Fatal(err)
	}
	if inside {
		t.Fatalf("control path %q must not be inside checkout %q", path, checkout)
	}
}

// @covers AC-4
func TestGuardProofRejectsChmodOnlyAndSamePrincipal(t *testing.T) {
	for _, proof := range []GuardProof{
		{Kind: GuardChmodOnly, AgentPrincipal: "host", HostPrincipal: "host"},
		{Kind: GuardSeparatePrincipal, AgentPrincipal: "host", HostPrincipal: "host"},
	} {
		if err := proof.Validate(); err == nil {
			t.Fatalf("Validate() accepted %#v", proof)
		}
	}

	if err := (GuardProof{Kind: GuardReadOnlyMount, AgentPrincipal: "agent", HostPrincipal: "host"}).Validate(); err != nil {
		t.Fatalf("read-only mount proof rejected: %v", err)
	}
}

// @covers AC-1, AC-3, AC-6
func TestPublisherPreservesCheckoutAgentsAndVerifiesMirror(t *testing.T) {
	tasksBase := t.TempDir()
	taskRoot := filepath.Join(tasksBase, "task-42")
	checkout := filepath.Join(taskRoot, "repository")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	repositoryAgents := filepath.Join(checkout, "AGENTS.md")
	const repositoryContent = "# Human repository rules\nDo not change this file.\n"
	if err := os.WriteFile(repositoryAgents, []byte(repositoryContent), 0o644); err != nil {
		t.Fatal(err)
	}

	binding := Binding{
		TaskID:              "task-42",
		WorkspaceID:         "workspace-1",
		EnvironmentID:       "environment-1",
		OwnershipGeneration: 7,
		Version:             3,
		Content:             "# Coordinator task rules\nUse the exact parent relation.\n",
		Guard:               GuardProof{Kind: GuardReadOnlyMount, AgentPrincipal: "agent", HostPrincipal: "host"},
	}
	controlPath, err := ManagedControlPath(tasksBase, "task-42", []string{checkout})
	if err != nil {
		t.Fatal(err)
	}
	binding.ControlPath = controlPath
	if err := Publish(binding); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	gotRepository, err := os.ReadFile(repositoryAgents)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRepository) != repositoryContent {
		t.Fatalf("repository AGENTS.md changed:\n got %q\nwant %q", gotRepository, repositoryContent)
	}
	if err := VerifyMirror(binding); err != nil {
		t.Fatalf("VerifyMirror() error = %v", err)
	}

	if err := os.WriteFile(controlPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMirror(binding); err == nil {
		t.Fatal("VerifyMirror() accepted tampered control file")
	}
}

// @covers AC-1 and AC-6
func TestPublisherRejectsControlPathInsideGitWorktreeWithoutCallerCheckoutHints(t *testing.T) {
	checkout := t.TempDir()
	if err := os.Mkdir(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	binding := Binding{
		TaskID: "task-42", WorkspaceID: "workspace-1", EnvironmentID: "environment-1",
		OwnershipGeneration: 1, Version: 1, Content: "Coordinator rule.",
		ControlPath: filepath.Join(checkout, ".kandev", "task-instructions", "AGENTS.md"),
		Guard:       GuardProof{Kind: GuardReadOnlyMount},
	}
	if err := Publish(binding); err == nil {
		t.Fatal("Publish() accepted a control path inside a Git worktree")
	}
}

// @covers AC-7 and the authoritative lineage contract.
func TestComposePromptContextOrdersRepositoryRulesBeforeTaskBinding(t *testing.T) {
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "AGENTS.md"), []byte("# Human root rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "service", "AGENTS.md"), []byte("# Human service rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lineage, err := ResolveLineage("task-child", "", "", AuthoritativeLineage{
		ParentTaskID: "task-parent", ReportToTaskID: "task-lead", ParentResolved: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{TaskID: "task-child", WorkspaceID: "workspace-1", EnvironmentID: "env-1", OwnershipGeneration: 2, Version: 4, Content: RenderTemplate(lineage, "Coordinate with the parent."), Guard: GuardProof{Kind: GuardReadOnlyMount}}
	context, err := ComposePromptContext(checkout, filepath.Join(checkout, "service"), binding)
	if err != nil {
		t.Fatal(err)
	}
	root := strings.Index(context, "Human root rule")
	service := strings.Index(context, "Human service rule")
	task := strings.Index(context, "Coordinate with the parent.")
	if root < 0 || service < root || task < service {
		t.Fatalf("unexpected instruction order:\n%s", context)
	}
	if !strings.Contains(context, "Parent task: task-parent") || !strings.Contains(context, "Report to: task-lead") {
		t.Fatalf("context did not use authoritative lineage:\n%s", context)
	}
}

// @covers parent-change/stale-parent rejection.
func TestResolveLineageRejectsUnresolvedOrStaleCallerParent(t *testing.T) {
	if _, err := ResolveLineage("task-child", "task-parent", "", AuthoritativeLineage{}); err == nil {
		t.Fatal("ResolveLineage accepted missing authoritative parent edge")
	}
	if _, err := ResolveLineage("task-child", "task-old", "", AuthoritativeLineage{ParentTaskID: "task-new", ParentResolved: true}); err == nil {
		t.Fatal("ResolveLineage accepted stale caller parent")
	}
}
