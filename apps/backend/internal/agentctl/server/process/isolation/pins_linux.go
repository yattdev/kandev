//go:build linux

package isolation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func validatePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return errors.New("isolation object path is not canonical")
	}
	return nil
}

func openPath(path string) (*os.File, unix.Stat_t, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, unix.Stat_t{}, err
	}
	return file, stat, nil
}

// Capture records the exact object expected at an absolute grant path.
func Capture(path string) (Identity, error) {
	if err := validatePath(path); err != nil {
		return Identity{}, err
	}
	file, stat, err := openPath(path)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return Identity{}, fmt.Errorf("open isolation object: %w", err)
	}
	if file != nil {
		defer func() { _ = file.Close() }()
	}
	parentPath := filepath.Dir(path)
	for {
		parent, parentStat, parentErr := openPath(parentPath)
		if parentErr == nil {
			_ = parent.Close()
			identity := Identity{Path: path, ParentPath: parentPath, ParentDev: parentStat.Dev, ParentIno: parentStat.Ino}
			if file != nil {
				identity.Exists = true
				identity.Dev, identity.Ino, identity.Mode = stat.Dev, stat.Ino, stat.Mode&unix.S_IFMT
			}
			return identity, nil
		}
		if !errors.Is(parentErr, unix.ENOENT) || parentPath == string(filepath.Separator) {
			return Identity{}, fmt.Errorf("open isolation parent: %w", parentErr)
		}
		parentPath = filepath.Dir(parentPath)
	}
}

// Pin reopens an attested path without following symlinks and retains its object.
func Pin(want Identity) (*Pinned, error) {
	if err := validatePath(want.Path); err != nil {
		return nil, err
	}
	got, err := Capture(want.Path)
	if err != nil || got != want {
		return nil, errors.New("isolation object changed before pinning")
	}
	parent, parentStat, err := openPath(want.ParentPath)
	if err != nil {
		return nil, fmt.Errorf("pin isolation parent: %w", err)
	}
	if parentStat.Dev != want.ParentDev || parentStat.Ino != want.ParentIno {
		_ = parent.Close()
		return nil, errors.New("isolation parent changed during pinning")
	}
	leaf, err := filepath.Rel(want.ParentPath, want.Path)
	if err != nil || leaf == "." || strings.HasPrefix(leaf, "..") {
		_ = parent.Close()
		return nil, errors.New("invalid isolation leaf")
	}
	file := parent
	if want.Exists {
		file, err = pinExisting(parent, leaf, want)
		if err != nil {
			_ = parent.Close()
			return nil, err
		}
		leaf = ""
	}
	pin := &Pinned{identity: want, file: file, leaf: leaf}
	if want.Exists {
		pin.parent = parent
	}
	if err := pin.Validate(); err != nil {
		_ = pin.Close()
		return nil, err
	}
	return pin, nil
}

func pinExisting(parent *os.File, leaf string, want Identity) (*os.File, error) {
	fd, err := unix.Openat2(int(parent.Fd()), leaf, &unix.OpenHow{
		Flags:   unix.O_PATH | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("pin isolation object: %w", err)
	}
	file := os.NewFile(uintptr(fd), want.Path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Dev != want.Dev || stat.Ino != want.Ino || stat.Mode&unix.S_IFMT != want.Mode {
		_ = file.Close()
		return nil, errors.New("isolation object changed during pinning")
	}
	return file, nil
}

// Validate must run immediately before installation to reject path replacement.
func (p *Pinned) Validate() error {
	got, err := Capture(p.identity.Path)
	if err != nil || got != p.identity {
		return errors.New("isolation object changed after pinning")
	}
	return nil
}
