// Package taskinstructions owns the non-Git projection of Coordinator task
// instructions. It deliberately treats a file permission bit as insufficient
// proof that an agent cannot modify the projection.
package taskinstructions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	controlDirectory = ".kandev/task-instructions"
	controlFilename  = "AGENTS.md"
	maxContentBytes  = 32 << 10
)

var (
	ErrGuardUnavailable = errors.New("task instruction guard is unavailable")
	ErrMirrorMismatch   = errors.New("task instruction mirror does not match binding")
)

// GuardKind identifies an executor proof that the agent cannot mutate the
// Host projection or reach it through a writable alias.
type GuardKind string

const (
	GuardChmodOnly         GuardKind = "chmod_only"
	GuardReadOnlyMount     GuardKind = "read_only_mount"
	GuardSeparatePrincipal GuardKind = "separate_principal"
	GuardProviderAttested  GuardKind = "provider_attested"
)

// GuardProof is supplied by the executor boundary after it has installed the
// projection. It is intentionally small so local mode cannot claim safety by
// setting a permission bit on a same-principal process.
type GuardProof struct {
	Kind           GuardKind
	HostPrincipal  string
	AgentPrincipal string
}

func (p GuardProof) Validate() error {
	switch p.Kind {
	case GuardReadOnlyMount, GuardProviderAttested:
		return nil
	case GuardSeparatePrincipal:
		if p.HostPrincipal != "" && p.AgentPrincipal != "" && p.HostPrincipal != p.AgentPrincipal {
			return nil
		}
	}
	return ErrGuardUnavailable
}

// Binding is the authoritative, task-environment-bound instruction record.
// The repository is authoritative; this structure is also the immutable input
// to a filesystem projection and prompt admission verification.
type Binding struct {
	TaskID              string
	WorkspaceID         string
	EnvironmentID       string
	OwnershipGeneration int64
	Version             int64
	Content             string
	Digest              string
	ControlPath         string
	Checkouts           []string
	Guard               GuardProof
}

func (b Binding) Validate() error {
	if !b.hasRequiredIdentity() {
		return errors.New("task instruction binding is incomplete")
	}
	if len(b.Content) > maxContentBytes || !utf8Content(b.Content) {
		return errors.New("task instruction content is invalid")
	}
	if err := b.validateControlPath(); err != nil {
		return err
	}
	if err := b.Guard.Validate(); err != nil {
		return err
	}
	if err := b.validateCheckoutSeparation(); err != nil {
		return err
	}
	if b.Digest != "" && b.Digest != Digest(b.Content) {
		return ErrMirrorMismatch
	}
	return nil
}

func (b Binding) hasRequiredIdentity() bool {
	return b.TaskID != "" && b.WorkspaceID != "" && b.EnvironmentID != "" &&
		b.OwnershipGeneration >= 1 && b.Version >= 1
}

func (b Binding) validateControlPath() error {
	if b.ControlPath == "" || filepath.Base(b.ControlPath) != controlFilename {
		return errors.New("task instruction control path is invalid")
	}
	insideGit, err := isInsideGitWorktree(b.ControlPath)
	if err != nil {
		return err
	}
	if insideGit {
		return errors.New("task instruction control path is inside a Git worktree")
	}
	return nil
}

func (b Binding) validateCheckoutSeparation() error {
	for _, checkout := range b.Checkouts {
		inside, err := isWithin(checkout, b.ControlPath)
		if err != nil {
			return err
		}
		if inside {
			return fmt.Errorf("task instruction control path is inside checkout %q", checkout)
		}
	}
	return nil
}

// Digest returns the canonical SHA-256 of the exact instruction content.
func Digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ManagedControlPath returns the stable control-file location for a managed
// worktree task root. It does not infer a root from a shared local checkout.
func ManagedControlPath(tasksBase, taskDirName string, checkouts []string) (string, error) {
	if tasksBase == "" || !safeSegment(taskDirName) {
		return "", errors.New("invalid managed task control root")
	}
	root := filepath.Join(tasksBase, taskDirName)
	path := filepath.Join(root, filepath.FromSlash(controlDirectory), controlFilename)
	for _, checkout := range checkouts {
		inside, err := isWithin(checkout, path)
		if err != nil {
			return "", err
		}
		if inside {
			return "", fmt.Errorf("managed task control path falls inside checkout %q", checkout)
		}
	}
	return path, nil
}

// Publish atomically projects a verified binding. It refuses symlinked control
// ancestors and never touches a repository checkout.
func Publish(binding Binding) error {
	if binding.Digest == "" {
		binding.Digest = Digest(binding.Content)
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	parent := filepath.Dir(binding.ControlPath)
	if err := mkdirNoSymlink(parent); err != nil {
		return err
	}
	if info, err := os.Lstat(binding.ControlPath); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("task instruction control file is not regular")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".agents-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(renderMirror(binding)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, binding.ControlPath); err != nil {
		return err
	}
	return VerifyMirror(binding)
}

// VerifyMirror proves the currently visible projection matches the current
// binding. Callers use it immediately before governed prompt admission.
func VerifyMirror(binding Binding) error {
	if binding.Digest == "" {
		binding.Digest = Digest(binding.Content)
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	info, err := os.Lstat(binding.ControlPath)
	if err != nil {
		return fmt.Errorf("read task instruction mirror: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ErrMirrorMismatch
	}
	data, err := os.ReadFile(binding.ControlPath)
	if err != nil {
		return err
	}
	if string(data) != renderMirror(binding) {
		return ErrMirrorMismatch
	}
	return nil
}

func renderMirror(binding Binding) string {
	return fmt.Sprintf("<!-- kandev-task-instructions task=%s workspace=%s environment=%s generation=%d version=%d sha256=%s -->\n\n%s",
		binding.TaskID, binding.WorkspaceID, binding.EnvironmentID, binding.OwnershipGeneration, binding.Version, binding.Digest, binding.Content)
}

func mkdirNoSymlink(path string) error {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	current := ""
	if filepath.IsAbs(clean) {
		current = string(filepath.Separator)
	}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe task instruction ancestor %q", current)
		}
	}
	return nil
}

func isWithin(root, path string) (bool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

func safeSegment(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value
}

func isInsideGitWorktree(path string) (bool, error) {
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	for {
		gitMarker := filepath.Join(dir, ".git")
		if _, err := os.Lstat(gitMarker); err == nil {
			return true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}

func utf8Content(content string) bool {
	return utf8.ValidString(content) && strings.IndexByte(content, 0) == -1
}
