package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Independent clones are accepted only through the operator registration
// installed by this deployment. Ordinary .git directories remain ambiguous.
// The verifier attests the exact live task/environment binding, unchanged
// retirement artifact, independent metadata, origin and selected branch.
func inspectAdoptedCheckout(path string, info os.FileInfo, verify func(string) error) linkedWorktreeInspection {
	if info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return linkedWorktreeInspection{class: linkedWorktreeAmbiguous, reason: "adopted checkout Git metadata is not an independent directory"}
	}
	if err := verify(path); err != nil {
		return linkedWorktreeInspection{class: linkedWorktreeAmbiguous, reason: "independent checkout has no valid operator adoption"}
	}
	return linkedWorktreeInspection{class: linkedWorktreeHealthy, adminPath: filepath.Join(path, ".git")}
}

func verifyOperatorAdoption(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/local/bin/kandev-workspace-registration", "check-adopted", path).Run()
}
