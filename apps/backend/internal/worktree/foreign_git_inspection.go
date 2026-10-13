package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	storageworkspaces "github.com/kandev/kandev/internal/system/storage/workspaces"
)

// ForeignGitInspectionStatus is the closed result set used by the disabled
// foreign-pointer evidence seam. It intentionally has no "clean" state.
type ForeignGitInspectionStatus string

const (
	ForeignGitInspectionPass    ForeignGitInspectionStatus = "PASS"
	ForeignGitInspectionBlocked ForeignGitInspectionStatus = "BLOCKED"
	ForeignGitInspectionUnknown ForeignGitInspectionStatus = "UNKNOWN"
)

// ForeignGitInspectionRequest names a persisted worktree. The caller cannot
// provide a checkout path: the manager reloads it by ID before opening it.
// This type is deliberately not registered with an HTTP or MCP route.
type ForeignGitInspectionRequest struct {
	RequestID                  string
	TaskID                     string
	WorktreeID                 string
	ExpectedWorktreeGeneration string
}

// ForeignGitInspectionEntry stores physical source identity without source
// bytes. The top-level .git pointer is reported separately in Pointer.
type ForeignGitInspectionEntry struct {
	Path          string      `json:"path"`
	Kind          string      `json:"kind"`
	Mode          os.FileMode `json:"mode"`
	Size          int64       `json:"size"`
	ContentSHA256 string      `json:"content_sha256,omitempty"`
	LinkSHA256    string      `json:"link_sha256,omitempty"`
}

// ForeignGitPointerReceipt does not dereference the administration target.
// A later, separately authorized comparison operation may bind its task owner
// and backlink; absence of that authority remains UNKNOWN here.
type ForeignGitPointerReceipt struct {
	AdminPath           string                     `json:"admin_path,omitempty"`
	Backlink            string                     `json:"backlink,omitempty"`
	Reciprocal          bool                       `json:"reciprocal"`
	ForeignTaskID       string                     `json:"foreign_task_id,omitempty"`
	ForeignTaskEvidence ForeignGitInspectionStatus `json:"foreign_task_evidence"`
	Status              ForeignGitInspectionStatus `json:"status"`
	ReasonCode          string                     `json:"reason_code"`
	PointerSHA256       string                     `json:"pointer_sha256,omitempty"`
}

// ForeignGitInspectionReceipt is a redacted evidence object. It contains no
// source bytes, Git index data, commit data, command output, or credentials.
type ForeignGitInspectionReceipt struct {
	RequestID                  string                      `json:"request_id"`
	TaskID                     string                      `json:"task_id"`
	TaskEnvironmentID          string                      `json:"task_environment_id,omitempty"`
	WorktreeID                 string                      `json:"worktree_id"`
	RepositoryID               string                      `json:"repository_id"`
	ObservedWorktreeGeneration string                      `json:"observed_worktree_generation"`
	CapturedAt                 time.Time                   `json:"captured_at"`
	Complete                   bool                        `json:"complete"`
	Overall                    ForeignGitInspectionStatus  `json:"overall"`
	Pointer                    ForeignGitPointerReceipt    `json:"pointer"`
	Entries                    []ForeignGitInspectionEntry `json:"entries"`
	TreeSHA256                 string                      `json:"tree_sha256,omitempty"`
	ReasonCodes                []string                    `json:"reason_codes"`
}

const foreignGitInspectionEntryLimit = 100_000
const foreignGitInspectionFileByteLimit int64 = 1 << 30
const foreignGitInspectionTotalByteLimit int64 = 4 << 30
const foreignGitInspectionPointerByteLimit int64 = 64 << 10
const foreignGitInspectionForeignAdminUnauthorized = "FOREIGN_ADMIN_UNAUTHORIZED"

// InspectForeignGitWorktree performs the internal evidence-only half of a
// recovery. It never invokes Git, opens the foreign admin target, or mutates
// either checkout. Route registration and cross-task authorization remain a
// separate security decision.
func (m *Manager) InspectForeignGitWorktree(ctx context.Context, request ForeignGitInspectionRequest) (*ForeignGitInspectionReceipt, error) {
	return m.inspectForeignGitWorktree(ctx, request, storageworkspaces.OpenDirectoryNoFollow)
}

func (m *Manager) inspectForeignGitWorktree(ctx context.Context, request ForeignGitInspectionRequest, open foreignGitDirectoryOpener) (*ForeignGitInspectionReceipt, error) {
	wt, generation, err := m.foreignGitInspectionWorktree(ctx, request)
	if err != nil {
		return nil, err
	}
	receipt := newForeignGitInspectionReceipt(request, wt, generation)
	owner, err := m.foreignGitInspectionOwner(ctx, wt, open)
	if err != nil {
		receipt.addReason("TASK_ROOT_OWNERSHIP_UNKNOWN")
		if ctx.Err() != nil {
			receipt.addReason("SOURCE_WALK_INCOMPLETE")
		}
		return receipt, nil
	}
	defer owner.close()
	root, err := owner.openCheckout(wt.Path)
	if err != nil {
		receipt.addReason("CHECKOUT_OPEN_FAILED")
		return receipt, nil
	}
	foreignGitInspectionCapture(ctx, root, wt.Path, receipt)
	if err := owner.verify(ctx, open); err != nil {
		receipt.addReason("TASK_ROOT_OWNERSHIP_CHANGED")
		receipt.Complete = false
		receipt.Overall = ForeignGitInspectionUnknown
	}
	if current, currentGeneration, reloadErr := m.foreignGitInspectionWorktree(ctx, request); reloadErr != nil || !foreignGitInspectionSameWorktree(wt, current) || currentGeneration != generation {
		receipt.addReason("WORKTREE_GENERATION_CHANGED")
		receipt.Complete = false
		receipt.Overall = ForeignGitInspectionUnknown
	}
	return receipt, nil
}

func (m *Manager) foreignGitInspectionTaskRoot(wt *Worktree) (string, error) {
	if wt == nil || strings.TrimSpace(wt.TaskDirName) == "" {
		return "", errors.New("worktree task-root ownership is unavailable")
	}
	tasksBase, err := m.config.ExpandedTasksBasePath()
	if err != nil {
		return "", fmt.Errorf("resolve tasks base path: %w", err)
	}
	tasksBase = filepath.Clean(tasksBase)
	relative, err := filepath.Rel(tasksBase, wt.Path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("worktree path lies outside the task root")
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) < 2 || parts[0] != wt.TaskDirName {
		return "", errors.New("worktree path conflicts with its recorded task root")
	}
	return filepath.Join(tasksBase, parts[0]), nil
}

func foreignGitInspectionSameWorktree(expected, current *Worktree) bool {
	return current != nil && expected.ID == current.ID && expected.TaskID == current.TaskID && expected.TaskDirName == current.TaskDirName && expected.Path == current.Path && expected.RepositoryID == current.RepositoryID && expected.TaskEnvironmentID == current.TaskEnvironmentID
}

func (m *Manager) foreignGitInspectionWorktree(ctx context.Context, request ForeignGitInspectionRequest) (*Worktree, string, error) {
	if m == nil || m.store == nil || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.TaskID) == "" ||
		strings.TrimSpace(request.WorktreeID) == "" || strings.TrimSpace(request.ExpectedWorktreeGeneration) == "" {
		return nil, "", fmt.Errorf("foreign Git inspection request identity is incomplete")
	}
	wt, err := m.store.GetWorktreeByID(ctx, request.WorktreeID)
	if err != nil {
		return nil, "", fmt.Errorf("load requested worktree: %w", err)
	}
	if wt == nil || wt.TaskID != request.TaskID || wt.Path == "" || wt.RepositoryID == "" {
		return nil, "", fmt.Errorf("requested foreign Git inspection inventory is unavailable")
	}
	generation := wt.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if request.ExpectedWorktreeGeneration != generation {
		return nil, "", fmt.Errorf("foreign Git inspection worktree generation is stale")
	}
	return wt, generation, nil
}

func newForeignGitInspectionReceipt(request ForeignGitInspectionRequest, wt *Worktree, generation string) *ForeignGitInspectionReceipt {
	return &ForeignGitInspectionReceipt{
		RequestID: request.RequestID, TaskID: wt.TaskID, TaskEnvironmentID: wt.TaskEnvironmentID,
		WorktreeID: wt.ID, RepositoryID: wt.RepositoryID, ObservedWorktreeGeneration: generation,
		CapturedAt: time.Now().UTC(), Overall: ForeignGitInspectionUnknown,
		Pointer: ForeignGitPointerReceipt{ForeignTaskEvidence: ForeignGitInspectionUnknown, Status: ForeignGitInspectionUnknown},
	}
}

func foreignGitInspectionDigestFile(file io.ReadCloser) (string, int64, error) {
	digest, size, err := foreignGitInspectionDigestFileLimit(context.Background(), file, foreignGitInspectionFileByteLimit)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return digest, size, err
}

var errForeignGitInspectionByteLimit = fmt.Errorf("foreign Git inspection byte limit exceeded")

func foreignGitInspectionDigestFileLimit(ctx context.Context, file io.ReadCloser, byteLimit int64) (string, int64, error) {
	statFile, ok := file.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return "", 0, fmt.Errorf("foreign Git inspection cannot verify file identity")
	}
	before, statErr := statFile.Stat()
	if statErr != nil {
		return "", 0, statErr
	}
	if before.Size() > byteLimit {
		return "", 0, errForeignGitInspectionByteLimit
	}
	h := sha256.New()
	size, copyErr := foreignGitInspectionCopyContext(ctx, h, file, byteLimit+1)
	after, afterErr := statFile.Stat()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if size > byteLimit {
		return "", 0, errForeignGitInspectionByteLimit
	}
	if afterErr != nil {
		return "", 0, afterErr
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || size != after.Size() {
		return "", 0, fmt.Errorf("foreign Git inspection file changed during capture")
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func foreignGitInspectionReadFile(ctx context.Context, file io.ReadCloser, limit int64) ([]byte, error) {
	statFile, ok := file.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return nil, fmt.Errorf("foreign Git inspection cannot verify file identity")
	}
	before, err := statFile.Stat()
	if err != nil {
		return nil, err
	}
	if before.Size() > limit {
		return nil, errForeignGitInspectionByteLimit
	}
	var contents strings.Builder
	contents.Grow(int(before.Size()))
	if _, err := foreignGitInspectionCopyContext(ctx, &contents, file, limit+1); err != nil {
		return nil, err
	}
	after, err := statFile.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || int64(contents.Len()) != after.Size() {
		return nil, errors.New("foreign Git inspection file changed during capture")
	}
	return []byte(contents.String()), nil
}

func foreignGitInspectionCopyContext(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return total, errForeignGitInspectionByteLimit
			}
			if _, err := dst.Write(buffer[:n]); err != nil {
				return total, err
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func foreignGitInspectionTreeDigest(entries []ForeignGitInspectionEntry) string {
	h := sha256.New()
	for _, entry := range entries {
		_, _ = io.WriteString(h, entry.Path+"\x00"+entry.Kind+"\x00"+entry.Mode.String()+"\x00")
		_, _ = io.WriteString(h, strconv.FormatInt(entry.Size, 10)+"\x00"+entry.ContentSHA256+"\x00"+entry.LinkSHA256+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func foreignGitInspectionSHA256(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (receipt *ForeignGitInspectionReceipt) addReason(reason string) {
	for _, existing := range receipt.ReasonCodes {
		if existing == reason {
			return
		}
	}
	receipt.ReasonCodes = append(receipt.ReasonCodes, reason)
}

func foreignGitInspectionHasReason(reasons []string, reason string) bool {
	for _, existing := range reasons {
		if existing == reason {
			return true
		}
	}
	return false
}
