package workspaces

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
)

type markerReadFile interface {
	io.ReadCloser
	io.Seeker
	Stat() (os.FileInfo, error)
}

type markerChangeStamp struct {
	seconds     int64
	nanoseconds int64
}

type markerFileState struct {
	info   os.FileInfo
	change markerChangeStamp
}

func readStableOwnershipMarker(ctx context.Context, handle DirectoryHandle, opened io.ReadCloser, limit int64) ([]byte, error) {
	file, ok := opened.(markerReadFile)
	if !ok {
		return nil, errors.New("ownership marker requires a stat-capable seekable descriptor")
	}
	before, err := ownershipMarkerFileState(file)
	if err != nil {
		return nil, err
	}
	encoded, err := readOwnershipMarkerChunks(ctx, file, limit)
	if err != nil {
		return nil, err
	}
	if err := verifyOwnershipMarkerState(file, before, len(encoded)); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// Reread the same descriptor. Content equality supplements change identity;
	// it does not establish quiescence or rule out every transient replacement.
	confirmed, err := readOwnershipMarkerChunks(ctx, file, limit)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded, confirmed) {
		return nil, errors.New("ownership marker content changed during read")
	}
	if err := handle.VerifyFileEntry(OwnershipMarkerFilename, file); err != nil {
		return nil, err
	}
	if err := verifyOwnershipMarkerState(file, before, len(confirmed)); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return encoded, nil
}

func ownershipMarkerFileState(file markerReadFile) (markerFileState, error) {
	info, err := file.Stat()
	if err != nil {
		return markerFileState{}, err
	}
	if !info.Mode().IsRegular() {
		return markerFileState{}, errors.New("ownership marker is not a regular file")
	}
	change, err := ownershipMarkerChangeStamp(file, info)
	if err != nil {
		return markerFileState{}, err
	}
	return markerFileState{info: info, change: change}, nil
}

func verifyOwnershipMarkerState(file markerReadFile, before markerFileState, size int) error {
	after, err := ownershipMarkerFileState(file)
	if err != nil {
		return err
	}
	if !os.SameFile(before.info, after.info) || before.info.Mode() != after.info.Mode() ||
		before.info.Size() != after.info.Size() || int64(size) != after.info.Size() ||
		!before.info.ModTime().Equal(after.info.ModTime()) || before.change != after.change {
		return errors.New("ownership marker changed during read")
	}
	return nil
}

// Cancellation is cooperative between bounded reads; arbitrary kernel I/O may
// still be uninterruptible. Unsupported descriptors fail closed.
func readOwnershipMarkerChunks(ctx context.Context, file io.Reader, limit int64) ([]byte, error) {
	var contents bytes.Buffer
	var chunk [4096]byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := file.Read(chunk[:])
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if int64(contents.Len())+int64(n) > limit {
			return nil, errors.New("workspace ownership marker exceeds limit")
		}
		contents.Write(chunk[:n])
		if errors.Is(err, io.EOF) {
			return contents.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
}
