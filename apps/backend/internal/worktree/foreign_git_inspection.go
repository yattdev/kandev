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
	"sort"
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
	wt, generation, err := m.foreignGitInspectionWorktree(ctx, request)
	if err != nil {
		return nil, err
	}
	receipt := newForeignGitInspectionReceipt(request, wt, generation)
	if err := m.foreignGitInspectionTaskRootOwner(ctx, wt); err != nil {
		receipt.addReason("TASK_ROOT_OWNERSHIP_UNKNOWN")
		if ctx.Err() != nil {
			receipt.addReason("SOURCE_WALK_INCOMPLETE")
		}
		return receipt, nil
	}
	root, err := openArchiveSourceDirectory(filepath.Dir(wt.Path), wt.Path)
	if err != nil {
		receipt.addReason("CHECKOUT_OPEN_FAILED")
		return receipt, nil
	}
	defer func() { _ = root.Close() }()
	foreignGitInspectionCapture(ctx, root, wt.Path, receipt)
	if current, currentGeneration, reloadErr := m.foreignGitInspectionWorktree(ctx, request); reloadErr != nil || !foreignGitInspectionSameWorktree(wt, current) || currentGeneration != generation {
		receipt.addReason("WORKTREE_GENERATION_CHANGED")
		receipt.Complete = false
		receipt.Overall = ForeignGitInspectionUnknown
	}
	return receipt, nil
}

func (m *Manager) foreignGitInspectionTaskRootOwner(ctx context.Context, wt *Worktree) error {
	taskRoot, err := m.foreignGitInspectionTaskRoot(wt)
	if err != nil {
		return err
	}
	handle, err := openArchiveSourceDirectory(filepath.Dir(taskRoot), taskRoot)
	if err != nil {
		return fmt.Errorf("open task-root ownership marker: %w", err)
	}
	defer func() { _ = handle.Close() }()
	marker, found, err := storageworkspaces.ReadOwnershipMarkerNoFollowContext(ctx, handle, foreignGitInspectionPointerByteLimit)
	if err != nil || !found {
		if err != nil {
			return fmt.Errorf("read task-root ownership marker: %w", err)
		}
		return errors.New("task-root ownership marker is missing")
	}
	if marker.TaskID != wt.TaskID || marker.TaskDirName != wt.TaskDirName {
		return errors.New("task-root ownership marker conflicts with worktree record")
	}
	return handle.VerifyPath(taskRoot)
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

func foreignGitInspectionCapture(ctx context.Context, root storageworkspaces.DirectoryHandle, checkout string, receipt *ForeignGitInspectionReceipt) {
	pointerErr := foreignGitInspectionPointer(ctx, root, checkout, &receipt.Pointer)
	if pointerErr != nil {
		receipt.Pointer.ReasonCode = "GIT_POINTER_UNAVAILABLE"
		receipt.addReason("GIT_POINTER_UNAVAILABLE")
	} else {
		receipt.Pointer.ReasonCode = foreignGitInspectionForeignAdminUnauthorized
		receipt.addReason(foreignGitInspectionForeignAdminUnauthorized)
	}
	var sourceBytes int64
	walkErr := foreignGitInspectionWalk(ctx, root, "", checkout, &receipt.Entries, receipt, &sourceBytes)
	if walkErr != nil {
		receipt.addReason("SOURCE_WALK_INCOMPLETE")
	}
	pathErr := root.VerifyPath(checkout)
	if ctxErr := ctx.Err(); ctxErr != nil {
		pathErr = ctxErr
	}
	if pathErr != nil {
		receipt.addReason("CHECKOUT_CHANGED")
	}
	receipt.Complete = pointerErr == nil && walkErr == nil && pathErr == nil && !foreignGitInspectionHasReason(receipt.ReasonCodes, "NESTED_GIT_BOUNDARY")
	sort.Slice(receipt.Entries, func(i, j int) bool { return receipt.Entries[i].Path < receipt.Entries[j].Path })
	receipt.TreeSHA256 = foreignGitInspectionTreeDigest(receipt.Entries)
	if receipt.Complete && len(receipt.ReasonCodes) == 1 && receipt.ReasonCodes[0] == foreignGitInspectionForeignAdminUnauthorized {
		receipt.Overall = ForeignGitInspectionBlocked
	} else {
		receipt.Overall = ForeignGitInspectionUnknown
	}
}

func foreignGitInspectionPointer(ctx context.Context, root storageworkspaces.DirectoryHandle, checkout string, pointer *ForeignGitPointerReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	mode, err := root.LstatEntry(recoveryGitDirName)
	if err != nil || !mode.IsRegular() {
		return fmt.Errorf("checkout .git pointer is unavailable")
	}
	file, err := root.OpenFile(recoveryGitDirName)
	if err != nil {
		return err
	}
	contents, err := foreignGitInspectionReadFile(ctx, file, foreignGitInspectionPointerByteLimit)
	if err == nil {
		err = root.VerifyFileEntry(recoveryGitDirName, file)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	pointer.PointerSHA256 = foreignGitInspectionSHA256(contents)
	admin, ok := archiveSourceGitDirPointer(string(contents), checkout)
	if !ok {
		return fmt.Errorf("checkout .git pointer is invalid")
	}
	pointer.AdminPath = admin
	return nil
}

func foreignGitInspectionWalk(ctx context.Context, directory storageworkspaces.DirectoryHandle, prefix, physicalPath string, entries *[]ForeignGitInspectionEntry, receipt *ForeignGitInspectionReceipt, sourceBytes *int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	children, err := directory.ReadDirN(foreignGitInspectionEntryLimit + 1)
	if err != nil {
		return err
	}
	if len(children) > foreignGitInspectionEntryLimit {
		receipt.addReason("SOURCE_ENTRY_LIMIT")
		return fmt.Errorf("foreign Git inspection directory entry limit reached")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := foreignGitInspectionWalkChild(ctx, directory, child.Name(), prefix, physicalPath, entries, receipt, sourceBytes); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := directory.ReadDirN(foreignGitInspectionEntryLimit + 1)
	if err != nil {
		return err
	}
	if len(current) > foreignGitInspectionEntryLimit || !foreignGitInspectionSameDirectoryEntries(children, current) {
		return errors.New("foreign Git inspection directory changed during capture")
	}
	return nil
}

func foreignGitInspectionSameDirectoryEntries(expected, current []os.DirEntry) bool {
	if len(expected) != len(current) {
		return false
	}
	sort.Slice(current, func(i, j int) bool { return current[i].Name() < current[j].Name() })
	for index := range expected {
		if expected[index].Name() != current[index].Name() || expected[index].Type() != current[index].Type() {
			return false
		}
	}
	return true
}

func foreignGitInspectionWalkChild(ctx context.Context, directory storageworkspaces.DirectoryHandle, name, prefix, physicalPath string, entries *[]ForeignGitInspectionEntry, receipt *ForeignGitInspectionReceipt, sourceBytes *int64) error {
	if prefix == "" && name == recoveryGitDirName {
		return nil
	}
	if len(*entries) >= foreignGitInspectionEntryLimit {
		receipt.addReason("SOURCE_ENTRY_LIMIT")
		return fmt.Errorf("foreign Git inspection entry limit reached")
	}
	mode, err := directory.LstatEntry(name)
	if err != nil {
		return err
	}
	path := foreignGitInspectionChildPath(prefix, name, receipt)
	entry, descend, err := foreignGitInspectionEntry(ctx, directory, name, path, mode, foreignGitInspectionRemainingBytes(*sourceBytes, receipt))
	if err != nil {
		if errors.Is(err, errForeignGitInspectionByteLimit) {
			receipt.addReason("SOURCE_BYTE_LIMIT")
		}
		return err
	}
	if entry.Kind == archiveSourceManifestKindFile {
		*sourceBytes += entry.Size
	}
	*entries = append(*entries, entry)
	if !descend {
		if _, err := directory.LstatEntry(name); err != nil {
			return err
		}
		if entry.Kind == archiveSourceManifestKindSymlink {
			target, err := directory.ReadLink(name)
			if err != nil || foreignGitInspectionSHA256([]byte(target)) != entry.LinkSHA256 {
				return errors.New("foreign Git inspection symbolic link changed during capture")
			}
		}
		return nil
	}
	return foreignGitInspectionWalkSubdirectory(ctx, directory, name, path, filepath.Join(physicalPath, name), entries, receipt, sourceBytes)
}

func foreignGitInspectionChildPath(prefix, name string, receipt *ForeignGitInspectionReceipt) string {
	if prefix == "" {
		return name
	}
	if name == recoveryGitDirName {
		receipt.addReason("NESTED_GIT_BOUNDARY")
	}
	return filepath.Join(prefix, name)
}

func foreignGitInspectionRemainingBytes(sourceBytes int64, receipt *ForeignGitInspectionReceipt) int64 {
	remaining := foreignGitInspectionTotalByteLimit - sourceBytes
	if remaining < 0 {
		receipt.addReason("SOURCE_BYTE_LIMIT")
	}
	return remaining
}

func foreignGitInspectionWalkSubdirectory(ctx context.Context, directory storageworkspaces.DirectoryHandle, name, path, physicalPath string, entries *[]ForeignGitInspectionEntry, receipt *ForeignGitInspectionReceipt, sourceBytes *int64) error {
	next, err := directory.OpenSubdirectory(name)
	if err != nil {
		return err
	}
	walkErr := foreignGitInspectionWalk(ctx, next, path, physicalPath, entries, receipt, sourceBytes)
	verifyErr := next.VerifyPath(physicalPath)
	closeErr := next.Close()
	if walkErr != nil {
		return walkErr
	}
	if verifyErr != nil {
		return verifyErr
	}
	return closeErr
}

func foreignGitInspectionEntry(ctx context.Context, directory storageworkspaces.DirectoryHandle, name, path string, mode os.FileMode, remainingBytes int64) (ForeignGitInspectionEntry, bool, error) {
	entry := ForeignGitInspectionEntry{Path: path, Mode: mode, Size: 0}
	switch {
	case mode.IsDir():
		entry.Kind = archiveSourceManifestKindDirectory
		return entry, true, nil
	case mode.IsRegular():
		file, err := directory.OpenFile(name)
		if err != nil {
			return ForeignGitInspectionEntry{}, false, err
		}
		limit := foreignGitInspectionFileByteLimit
		if remainingBytes < limit {
			limit = remainingBytes
		}
		digest, size, err := foreignGitInspectionDigestFileLimit(ctx, file, limit)
		if err == nil {
			err = directory.VerifyFileEntry(name, file)
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return ForeignGitInspectionEntry{}, false, err
		}
		entry.Kind, entry.ContentSHA256, entry.Size = archiveSourceManifestKindFile, digest, size
		return entry, false, nil
	case mode&os.ModeSymlink != 0:
		target, err := directory.ReadLink(name)
		if err != nil {
			return ForeignGitInspectionEntry{}, false, err
		}
		entry.Kind, entry.LinkSHA256, entry.Size = archiveSourceManifestKindSymlink, foreignGitInspectionSHA256([]byte(target)), int64(len(target))
		return entry, false, nil
	default:
		return ForeignGitInspectionEntry{}, false, fmt.Errorf("unsupported source file type at %q", path)
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
