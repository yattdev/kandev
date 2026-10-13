package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"

	storageworkspaces "github.com/kandev/kandev/internal/system/storage/workspaces"
)

type foreignGitInspectionWalkState struct {
	receipt *ForeignGitInspectionReceipt
	objects map[string]storageworkspaces.ObjectSnapshot
	bytes   int64
}

func foreignGitInspectionCapture(ctx context.Context, root storageworkspaces.DirectoryHandle, checkout string, receipt *ForeignGitInspectionReceipt) {
	first, err := foreignGitInspectionObserve(ctx, root, checkout, receipt)
	if err != nil {
		receipt.addReason("SOURCE_WALK_INCOMPLETE")
	}
	var verifyErr error
	if err == nil {
		confirmed := &ForeignGitInspectionReceipt{Pointer: receipt.Pointer}
		second, recheckErr := foreignGitInspectionObserve(ctx, root, checkout, confirmed)
		verifyErr = recheckErr
		if verifyErr == nil && (!slices.Equal(receipt.Entries, confirmed.Entries) ||
			receipt.Pointer != confirmed.Pointer || !foreignGitInspectionSameObjects(first, second)) {
			verifyErr = errors.New("source objects changed during final inspection")
		}
		if verifyErr != nil {
			receipt.addReason("SOURCE_REVALIDATION_FAILED")
		}
	}
	receipt.Complete = err == nil && verifyErr == nil && !foreignGitInspectionHasReason(receipt.ReasonCodes, "NESTED_GIT_BOUNDARY")
	receipt.TreeSHA256 = foreignGitInspectionTreeDigest(receipt.Entries)
	if receipt.Complete {
		receipt.Overall = ForeignGitInspectionBlocked
	} else {
		receipt.Overall = ForeignGitInspectionUnknown
	}
}

func foreignGitInspectionSameObjects(first, second map[string]storageworkspaces.ObjectSnapshot) bool {
	if len(first) != len(second) {
		return false
	}
	for path, expected := range first {
		if current, ok := second[path]; !ok || expected != current {
			return false
		}
	}
	return true
}

func foreignGitInspectionObserve(ctx context.Context, root storageworkspaces.DirectoryHandle, checkout string, receipt *ForeignGitInspectionReceipt) (map[string]storageworkspaces.ObjectSnapshot, error) {
	directory, ok := root.(storageworkspaces.IdentityDirectoryHandle)
	if !ok {
		return nil, errors.New("native directory identity is unavailable")
	}
	state := &foreignGitInspectionWalkState{receipt: receipt, objects: make(map[string]storageworkspaces.ObjectSnapshot)}
	if err := state.pointer(ctx, directory, checkout); err != nil {
		receipt.Pointer.ReasonCode = "GIT_POINTER_UNAVAILABLE"
		receipt.addReason("GIT_POINTER_UNAVAILABLE")
		return state.objects, err
	}
	receipt.Pointer.ReasonCode = foreignGitInspectionForeignAdminUnauthorized
	receipt.addReason(foreignGitInspectionForeignAdminUnauthorized)
	if err := state.walk(ctx, directory, "", checkout); err != nil {
		return state.objects, err
	}
	sort.Slice(receipt.Entries, func(i, j int) bool { return receipt.Entries[i].Path < receipt.Entries[j].Path })
	return state.objects, ctx.Err()
}

func (state *foreignGitInspectionWalkState) pointer(ctx context.Context, directory storageworkspaces.IdentityDirectoryHandle, checkout string) error {
	file, err := directory.OpenFile(recoveryGitDirName)
	if err != nil {
		return err
	}
	before, err := storageworkspaces.SnapshotFile(file)
	var contents []byte
	if err == nil {
		contents, err = foreignGitInspectionReadFile(ctx, file, foreignGitInspectionPointerByteLimit)
	}
	if err == nil {
		err = foreignGitInspectionVerifyFile(directory, recoveryGitDirName, file, before)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	admin, ok := archiveSourceGitDirPointer(string(contents), checkout)
	if !ok {
		return errors.New("checkout .git pointer is invalid")
	}
	state.objects[recoveryGitDirName] = before
	state.receipt.Pointer.PointerSHA256 = foreignGitInspectionSHA256(contents)
	state.receipt.Pointer.AdminPath = admin
	return nil
}

func (state *foreignGitInspectionWalkState) walk(ctx context.Context, directory storageworkspaces.IdentityDirectoryHandle, prefix, physicalPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := directory.Snapshot()
	if err != nil {
		return err
	}
	state.objects[prefix] = before
	children, err := directory.ReadDirN(foreignGitInspectionEntryLimit + 1)
	if err != nil {
		return err
	}
	if len(children) > foreignGitInspectionEntryLimit {
		state.receipt.addReason("SOURCE_ENTRY_LIMIT")
		return errors.New("source directory entry limit reached")
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := state.child(ctx, directory, child.Name(), prefix, physicalPath); err != nil {
			return err
		}
	}
	after, err := directory.Snapshot()
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("source directory changed during capture")
	}
	return directory.VerifyPath(physicalPath)
}

func (state *foreignGitInspectionWalkState) child(ctx context.Context, directory storageworkspaces.IdentityDirectoryHandle, name, prefix, physicalPath string) error {
	if prefix == "" && name == recoveryGitDirName {
		return nil
	}
	if len(state.receipt.Entries) >= foreignGitInspectionEntryLimit {
		state.receipt.addReason("SOURCE_ENTRY_LIMIT")
		return errors.New("source entry limit reached")
	}
	before, err := directory.SnapshotEntry(name)
	if err != nil {
		return err
	}
	path := name
	if prefix != "" {
		path = filepath.Join(prefix, name)
		if name == recoveryGitDirName {
			state.receipt.addReason("NESTED_GIT_BOUNDARY")
		}
	}
	entry := ForeignGitInspectionEntry{Path: path, Mode: before.Mode}
	switch {
	case before.Mode.IsDir():
		entry.Kind = archiveSourceManifestKindDirectory
		state.receipt.Entries = append(state.receipt.Entries, entry)
		return state.subdirectory(ctx, directory, name, path, filepath.Join(physicalPath, name), before)
	case before.Mode.IsRegular():
		entry, err = state.file(ctx, directory, name, entry, before)
	case before.Mode&os.ModeSymlink != 0:
		entry, err = foreignGitInspectionLink(directory, name, entry, before)
	default:
		return fmt.Errorf("unsupported source file type at %q", path)
	}
	if err != nil {
		return err
	}
	state.objects[path] = before
	state.receipt.Entries = append(state.receipt.Entries, entry)
	return nil
}

func (state *foreignGitInspectionWalkState) subdirectory(ctx context.Context, parent storageworkspaces.IdentityDirectoryHandle, name, path, physicalPath string, expected storageworkspaces.ObjectSnapshot) error {
	opened, err := parent.OpenSubdirectory(name)
	if err != nil {
		return err
	}
	defer func() { _ = opened.Close() }()
	next, ok := opened.(storageworkspaces.IdentityDirectoryHandle)
	if !ok {
		return errors.New("native directory identity is unavailable")
	}
	before, err := next.Snapshot()
	if err != nil || before != expected {
		return errors.New("source directory entry changed before descent")
	}
	if err := state.walk(ctx, next, path, physicalPath); err != nil {
		return err
	}
	after, err := parent.SnapshotEntry(name)
	if err != nil || after != before {
		return errors.New("source directory entry changed after descent")
	}
	return nil
}

func (state *foreignGitInspectionWalkState) file(ctx context.Context, directory storageworkspaces.IdentityDirectoryHandle, name string, entry ForeignGitInspectionEntry, expected storageworkspaces.ObjectSnapshot) (ForeignGitInspectionEntry, error) {
	file, err := directory.OpenFile(name)
	if err != nil {
		return entry, err
	}
	before, err := storageworkspaces.SnapshotFile(file)
	if err == nil && before != expected {
		err = errors.New("source file changed before content read")
	}
	limit := min(foreignGitInspectionFileByteLimit, foreignGitInspectionTotalByteLimit-state.bytes)
	if err == nil {
		entry.ContentSHA256, entry.Size, err = foreignGitInspectionDigestFileLimit(ctx, file, limit)
	}
	if err == nil {
		err = foreignGitInspectionVerifyFile(directory, name, file, before)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if errors.Is(err, errForeignGitInspectionByteLimit) {
		state.receipt.addReason("SOURCE_BYTE_LIMIT")
	}
	entry.Mode, entry.Kind = before.Mode, archiveSourceManifestKindFile
	state.bytes += entry.Size
	return entry, err
}

func foreignGitInspectionLink(directory storageworkspaces.IdentityDirectoryHandle, name string, entry ForeignGitInspectionEntry, expected storageworkspaces.ObjectSnapshot) (ForeignGitInspectionEntry, error) {
	target, observed, err := directory.ReadLinkSnapshot(name)
	if err != nil {
		return entry, err
	}
	if observed != expected {
		return entry, errors.New("source link changed before content read")
	}
	entry.Kind, entry.Mode = archiveSourceManifestKindSymlink, observed.Mode
	entry.LinkSHA256, entry.Size = foreignGitInspectionSHA256([]byte(target)), int64(len(target))
	return entry, nil
}

func foreignGitInspectionVerifyFile(directory storageworkspaces.IdentityDirectoryHandle, name string, file io.ReadCloser, expected storageworkspaces.ObjectSnapshot) error {
	after, err := storageworkspaces.SnapshotFile(file)
	if err != nil {
		return err
	}
	current, err := directory.SnapshotEntry(name)
	if err != nil {
		return err
	}
	if expected != after || expected != current {
		return errors.New("source file object changed during capture")
	}
	return nil
}
