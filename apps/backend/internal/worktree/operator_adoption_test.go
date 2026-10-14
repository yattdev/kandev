package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOperatorAdoptionRequiresIndependentMetadataAndAttestation(t *testing.T) {
	root := t.TempDir()
	metadata := filepath.Join(root, ".git")
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(metadata)
	if err != nil {
		t.Fatal(err)
	}
	denied := func(string) error { return errors.New("unregistered clone") }
	if result := inspectAdoptedCheckout(root, info, denied); result.class != linkedWorktreeAmbiguous {
		t.Fatal("unregistered independent clone was accepted")
	}
	called := false
	verified := func(path string) error {
		called = true
		if path != root {
			t.Fatal("wrong checkout attested")
		}
		return nil
	}
	if result := inspectAdoptedCheckout(root, info, verified); result.class != linkedWorktreeHealthy || result.adminPath != metadata || !called {
		t.Fatal("attested clone was rejected")
	}
	called = false
	if result := inspectAdoptedCheckout(root, nil, verified); result.class != linkedWorktreeAmbiguous || called {
		t.Fatal("missing metadata was attested")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(metadata, link); err != nil {
		t.Skip(err)
	}
	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if result := inspectAdoptedCheckout(root, linkInfo, verified); result.class != linkedWorktreeAmbiguous || called {
		t.Fatal("symlink metadata was attested")
	}
}

// Opt-in read-only verification against a live operator-repaired task. It
// opens the existing directory handle and performs no Git or DB writes.
func TestOperatorAdoptionLiveReuse(t *testing.T) {
	path := os.Getenv("KANDEV_TEST_ADOPTED_CHECKOUT")
	if path == "" {
		t.Skip("live repaired checkout not supplied")
	}
	taskID := os.Getenv("KANDEV_TEST_ADOPTED_TASK_ID")
	if taskID == "" {
		t.Fatal("KANDEV_TEST_ADOPTED_TASK_ID must identify the repaired task")
	}
	m := &Manager{config: Config{TasksBasePath: "/data/tasks"}}
	wt := &Worktree{TaskID: taskID, TaskDirName: filepath.Base(filepath.Dir(path))}
	handle, valid, err := m.openReusableWorktreePath(path, wt)
	if handle != nil {
		t.Cleanup(func() { _ = handle.Close() })
	}
	if err != nil || !valid {
		t.Fatalf("attested attach-only reuse failed: valid=%v err=%v", valid, err)
	}
	if !m.IsValid(path) {
		t.Fatal("attested integrity admission failed")
	}
}
