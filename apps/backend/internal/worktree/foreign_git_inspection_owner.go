package worktree

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	storageworkspaces "github.com/kandev/kandev/internal/system/storage/workspaces"
)

type foreignGitDirectoryOpener func(string, string) (storageworkspaces.DirectoryHandle, error)

type foreignGitOwnedDirectory struct {
	handle   storageworkspaces.IdentityDirectoryHandle
	path     string
	name     string
	snapshot storageworkspaces.ObjectSnapshot
}

type foreignGitInspectionOwner struct {
	directories []foreignGitOwnedDirectory
	markerFile  io.ReadCloser
	markerState storageworkspaces.ObjectSnapshot
	marker      storageworkspaces.OwnershipMarker
}

func (owner *foreignGitInspectionOwner) close() {
	if owner.markerFile != nil {
		_ = owner.markerFile.Close()
	}
	for index := len(owner.directories) - 1; index >= 0; index-- {
		_ = owner.directories[index].handle.Close()
	}
}

func (m *Manager) foreignGitInspectionOwner(ctx context.Context, wt *Worktree, open foreignGitDirectoryOpener) (*foreignGitInspectionOwner, error) {
	path, err := m.foreignGitInspectionTaskRoot(wt)
	if err != nil {
		return nil, err
	}
	opened, err := open(filepath.Dir(path), path)
	if err != nil {
		return nil, err
	}
	root, ok := opened.(storageworkspaces.IdentityDirectoryHandle)
	if !ok {
		_ = opened.Close()
		return nil, errors.New("native task-root identity is unavailable")
	}
	owner := &foreignGitInspectionOwner{directories: []foreignGitOwnedDirectory{{handle: root, path: path}}}
	if err := owner.admitMarker(ctx, wt); err != nil {
		owner.close()
		return nil, err
	}
	return owner, nil
}

func (owner *foreignGitInspectionOwner) admitMarker(ctx context.Context, wt *Worktree) error {
	root := &owner.directories[0]
	var err error
	root.snapshot, err = root.handle.Snapshot()
	if err != nil {
		return err
	}
	owner.markerFile, err = root.handle.OpenFile(storageworkspaces.OwnershipMarkerFilename)
	if err != nil {
		return err
	}
	owner.markerState, err = storageworkspaces.SnapshotFile(owner.markerFile)
	if err != nil {
		return err
	}
	marker, found, err := storageworkspaces.ReadOwnershipMarkerNoFollowContext(ctx, root.handle, foreignGitInspectionPointerByteLimit)
	if err != nil {
		return err
	}
	if !found || marker.TaskID != wt.TaskID || marker.TaskDirName != wt.TaskDirName {
		return errors.New("task-root ownership marker conflicts with worktree record")
	}
	owner.marker = marker
	if err := foreignGitInspectionVerifyFile(root.handle, storageworkspaces.OwnershipMarkerFilename, owner.markerFile, owner.markerState); err != nil {
		return err
	}
	return root.handle.VerifyPath(root.path)
}

func (owner *foreignGitInspectionOwner) openCheckout(checkout string) (storageworkspaces.DirectoryHandle, error) {
	root := owner.directories[0]
	relative, err := filepath.Rel(root.path, checkout)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("checkout lies outside the authenticated task root")
	}
	for _, name := range strings.Split(relative, string(filepath.Separator)) {
		parent := owner.directories[len(owner.directories)-1]
		before, err := parent.handle.SnapshotEntry(name)
		if err != nil {
			return nil, err
		}
		opened, err := parent.handle.OpenSubdirectory(name)
		if err != nil {
			return nil, err
		}
		next, ok := opened.(storageworkspaces.IdentityDirectoryHandle)
		if !ok {
			_ = opened.Close()
			return nil, errors.New("native checkout identity is unavailable")
		}
		current, err := next.Snapshot()
		if err != nil || current != before {
			_ = next.Close()
			return nil, errors.New("checkout entry changed during descent")
		}
		owner.directories = append(owner.directories, foreignGitOwnedDirectory{
			handle: next, path: filepath.Join(parent.path, name), name: name, snapshot: current,
		})
	}
	return owner.directories[len(owner.directories)-1].handle, nil
}

func (owner *foreignGitInspectionOwner) verify(ctx context.Context, open foreignGitDirectoryOpener) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root := owner.directories[0]
	// Reopen every lexical component without following links, while the original
	// authenticated root and checkout chain remain pinned.
	current, err := open(filepath.Dir(root.path), root.path)
	if err != nil {
		return err
	}
	defer func() { _ = current.Close() }()
	identity, ok := current.(storageworkspaces.IdentityDirectoryHandle)
	if !ok {
		return errors.New("native task-root identity is unavailable")
	}
	observed, err := identity.Snapshot()
	if err != nil || observed != root.snapshot {
		return errors.New("authenticated task-root path changed during capture")
	}
	if err := owner.verifyDirectories(ctx); err != nil {
		return err
	}
	if err := foreignGitInspectionVerifyFile(root.handle, storageworkspaces.OwnershipMarkerFilename, owner.markerFile, owner.markerState); err != nil {
		return err
	}
	marker, found, err := storageworkspaces.ReadOwnershipMarkerNoFollowContext(ctx, root.handle, foreignGitInspectionPointerByteLimit)
	if err != nil || !found || marker != owner.marker {
		return errors.New("authenticated ownership marker changed during capture")
	}
	if err := foreignGitInspectionVerifyFile(root.handle, storageworkspaces.OwnershipMarkerFilename, owner.markerFile, owner.markerState); err != nil {
		return err
	}
	return ctx.Err()
}

func (owner *foreignGitInspectionOwner) verifyDirectories(ctx context.Context) error {
	for index, directory := range owner.directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		observed, err := directory.handle.Snapshot()
		if err != nil || observed != directory.snapshot {
			return errors.New("authenticated directory object changed during capture")
		}
		if index > 0 {
			observed, err = owner.directories[index-1].handle.SnapshotEntry(directory.name)
			if err != nil || observed != directory.snapshot {
				return errors.New("authenticated directory chain changed during capture")
			}
		}
	}
	return nil
}
