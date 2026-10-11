package taskinstructions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AuthoritativeLineage is loaded from the task relation store. Caller-provided
// parent and report-to IDs are hints only and never become instruction facts.
type AuthoritativeLineage struct {
	ParentTaskID   string
	ReportToTaskID string
	ParentResolved bool
}

// Lineage is the safe form included in generated instruction content.
type Lineage struct {
	TaskID         string
	ParentTaskID   string
	ReportToTaskID string
}

// ResolveLineage rejects an absent authoritative edge and any stale caller
// value. A Coordinator must explicitly choose a scoped binding before issuing
// an instruction document for a task whose parent relation cannot be read.
func ResolveLineage(taskID, requestedParent, requestedReportTo string, authoritative AuthoritativeLineage) (Lineage, error) {
	if taskID == "" || !authoritative.ParentResolved {
		return Lineage{}, errors.New("task parent relation is unresolved")
	}
	if requestedParent != "" && requestedParent != authoritative.ParentTaskID {
		return Lineage{}, errors.New("caller parent relation is stale")
	}
	if requestedReportTo != "" && requestedReportTo != authoritative.ReportToTaskID {
		return Lineage{}, errors.New("caller report-to relation is stale")
	}
	return Lineage{TaskID: taskID, ParentTaskID: authoritative.ParentTaskID, ReportToTaskID: authoritative.ReportToTaskID}, nil
}

// RenderTemplate adds the Host-validated relationship fields to Coordinator
// content. Parent and report-to lines are intentionally explicit even when
// empty, so unresolved values cannot be silently represented as source text.
func RenderTemplate(lineage Lineage, content string) string {
	return fmt.Sprintf("Task: %s\nParent task: %s\nReport to: %s\n\n%s", lineage.TaskID, lineage.ParentTaskID, lineage.ReportToTaskID, content)
}

// RepositorySource is a Host-read instruction source. The relative path and
// exact bytes are both carried to preserve source hierarchy for providers that
// do not discover AGENTS.md themselves.
type RepositorySource struct {
	Path    string
	Content string
}

// DiscoverRepositorySources collects AGENTS.md at the checkout root and each
// ancestor of workDir inside that checkout. It never reads a sibling checkout.
func DiscoverRepositorySources(checkout, workDir string) ([]RepositorySource, error) {
	inside, err := isWithin(checkout, workDir)
	if err != nil || !inside {
		return nil, errors.New("working directory is outside checkout")
	}
	checkout, err = filepath.Abs(checkout)
	if err != nil {
		return nil, err
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for dir := workDir; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == checkout {
			break
		}
	}
	for left, right := 0, len(dirs)-1; left < right; left, right = left+1, right-1 {
		dirs[left], dirs[right] = dirs[right], dirs[left]
	}
	sources := make([]RepositorySource, 0, len(dirs))
	for _, dir := range dirs {
		path := filepath.Join(dir, controlFilename)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("repository instruction %q is not a regular file", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(checkout, path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, RepositorySource{Path: filepath.ToSlash(rel), Content: string(content)})
	}
	return sources, nil
}

// ComposePromptContext creates trusted instruction context for a single final
// provider admission. Repository rules appear first; the task-local addendum
// follows and cannot replace their source identity.
func ComposePromptContext(checkout, workDir string, binding Binding) (string, error) {
	if binding.TaskID == "" || binding.WorkspaceID == "" || binding.EnvironmentID == "" || binding.OwnershipGeneration < 1 || binding.Version < 1 {
		return "", errors.New("task instruction binding is incomplete")
	}
	if len(binding.Content) > maxContentBytes || !utf8Content(binding.Content) {
		return "", errors.New("task instruction content is invalid")
	}
	sources, err := DiscoverRepositorySources(checkout, workDir)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	result.WriteString("## Repository instructions\n")
	if len(sources) == 0 {
		result.WriteString("No applicable repository AGENTS.md files were found.\n")
	}
	for _, source := range sources {
		fmt.Fprintf(&result, "\n### %s\n%s\n", source.Path, source.Content)
	}
	fmt.Fprintf(&result, "\n## Task-local Coordinator instructions\nTask binding: task=%s workspace=%s environment=%s generation=%d version=%d sha256=%s\n\n%s\n",
		binding.TaskID, binding.WorkspaceID, binding.EnvironmentID, binding.OwnershipGeneration, binding.Version, Digest(binding.Content), binding.Content)
	return result.String(), nil
}
