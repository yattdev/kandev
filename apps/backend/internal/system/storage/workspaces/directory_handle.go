package workspaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// DirectoryHandle keeps a directory pinned while a worktree decision is made.
// Implementations open every path component without following links and use
// the resulting descriptor or native handle for subsequent reads, writes, and
// removal. This prevents a path replacement between validation and the side
// effect from redirecting the operation to another task root.
type DirectoryHandle interface {
	Close() error
	VerifyPath(path string) error
	IsValidWorktree() bool
	RemoveDirectory(ctx context.Context) error
	OpenFile(name string) (io.ReadCloser, error)
	OpenSubdirectory(name string) (DirectoryHandle, error)
	LstatEntry(name string) (os.FileMode, error)
	ReadLink(name string) (string, error)
	ReadDir() ([]os.DirEntry, error)
	// ReadDirN returns at most n entries from a duplicate pinned directory
	// handle. It lets read-only callers enforce their own allocation limit.
	ReadDirN(n int) ([]os.DirEntry, error)
	// VerifyFileEntry confirms that name still names the opened regular file.
	VerifyFileEntry(name string, file io.ReadCloser) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, mode os.FileMode) error
}

// ReadOwnershipMarkerNoFollow reads and validates a marker through an already
// pinned directory handle. Callers that require ownership proof must reject a
// missing marker rather than relying on legacy compatibility behavior.
func ReadOwnershipMarkerNoFollow(handle DirectoryHandle) (OwnershipMarker, bool, error) {
	return ReadOwnershipMarkerNoFollowContext(context.Background(), handle, 64<<10)
}

// ReadOwnershipMarkerNoFollowContext bounds and cancels a marker read through
// a pinned task-root handle.
func ReadOwnershipMarkerNoFollowContext(ctx context.Context, handle DirectoryHandle, limit int64) (OwnershipMarker, bool, error) {
	if handle == nil {
		return OwnershipMarker{}, false, errors.New("workspace ownership marker requires an opened directory")
	}
	if err := ctx.Err(); err != nil {
		return OwnershipMarker{}, false, err
	}
	if limit < 1 {
		return OwnershipMarker{}, false, errors.New("workspace ownership marker limit is invalid")
	}
	file, err := handle.OpenFile(OwnershipMarkerFilename)
	if errors.Is(err, os.ErrNotExist) {
		return OwnershipMarker{}, false, nil
	}
	if err != nil {
		return OwnershipMarker{}, false, err
	}
	defer func() { _ = file.Close() }()
	encoded, err := readStableOwnershipMarker(ctx, handle, file, limit)
	if err != nil {
		return OwnershipMarker{}, false, err
	}
	var marker OwnershipMarker
	if err := json.Unmarshal(encoded, &marker); err != nil {
		return OwnershipMarker{}, false, fmt.Errorf("decode workspace ownership marker: %w", err)
	}
	if marker.TaskID == "" || marker.TaskDirName == "" ||
		(marker.LayoutVersion != LayoutVersionSemantic && marker.LayoutVersion != LayoutVersionScratch) {
		return OwnershipMarker{}, false, errors.New("invalid workspace ownership marker fields")
	}
	return marker, true, nil
}

// WriteOwnershipMarkerNoFollow writes an ownership marker through an already
// opened task-root handle. The caller must verify the handle still identifies
// the expected lexical path before calling this function.
func WriteOwnershipMarkerNoFollow(handle DirectoryHandle, marker OwnershipMarker) error {
	if handle == nil {
		return errors.New("workspace ownership marker requires an opened directory")
	}
	if err := normalizeOwnershipMarker(&marker); err != nil {
		return err
	}
	matched, err := existingMarkerMatchesHandle(handle, marker)
	if err != nil {
		return err
	}
	if matched {
		return nil
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode workspace ownership marker: %w", err)
	}
	if err := handle.WriteFile(OwnershipMarkerFilename, encoded, 0o600); err != nil {
		return fmt.Errorf("install workspace ownership marker: %w", err)
	}
	return nil
}

func existingMarkerMatchesHandle(handle DirectoryHandle, marker OwnershipMarker) (bool, error) {
	encoded, err := handle.ReadFile(OwnershipMarkerFilename)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var existing OwnershipMarker
	if err := json.Unmarshal(encoded, &existing); err != nil {
		return false, fmt.Errorf("decode workspace ownership marker: %w", err)
	}
	if existing.TaskID == "" || existing.TaskDirName == "" ||
		(existing.LayoutVersion != LayoutVersionSemantic && existing.LayoutVersion != LayoutVersionScratch) {
		return false, errors.New("invalid workspace ownership marker fields")
	}
	if existing.TaskID != marker.TaskID || existing.TaskDirName != marker.TaskDirName || existing.LayoutVersion != marker.LayoutVersion {
		return false, errors.New("workspace ownership marker conflicts with requested task root")
	}
	if existing.WorkspaceID != "" && marker.WorkspaceID != "" && existing.WorkspaceID != marker.WorkspaceID {
		return false, errors.New("workspace ownership marker conflicts with requested workspace")
	}
	return true, nil
}

func validateDirectoryEntryName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("invalid directory entry name: %q", name)
	}
	return nil
}
