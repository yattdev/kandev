package lifecycle

import (
	"testing"

	"github.com/kandev/kandev/internal/worktree"
)

func TestDockerCloneInsideRejectsHostGitMetadataProjection(t *testing.T) {
	executor := &DockerExecutor{logger: newTestDockerLogger()}
	_, err := executor.buildContainerLaunchConfig(&ExecutorCreateRequest{
		GitMetadataProjections: []*worktree.GitMetadataProjection{{}},
	})
	if err == nil {
		t.Fatal("clone-inside Docker must reject a host Git metadata projection")
	}
}
