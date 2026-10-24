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

// CapturePinned records identity while retaining the first opened object and
// parent handles. A later path check cannot recycle either held inode.
func CapturePinned(path string) (Identity, *Pinned, error) {
	if err := validatePath(path); err != nil {
		return Identity{}, nil, err
	}
	file, stat, err := openPath(path)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return Identity{}, nil, err
	}
	parentPath := filepath.Dir(path)
	var parent *os.File
	var parentStat unix.Stat_t
	for {
		parent, parentStat, err = openPath(parentPath)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.ENOENT) || parentPath == string(filepath.Separator) {
			if file != nil {
				_ = file.Close()
			}
			return Identity{}, nil, fmt.Errorf("open isolation parent: %w", err)
		}
		parentPath = filepath.Dir(parentPath)
	}
	identity := Identity{Path: path, ParentPath: parentPath, ParentDev: parentStat.Dev, ParentIno: parentStat.Ino}
	pin := &Pinned{identity: identity, file: parent}
	if file != nil {
		identity.Exists, identity.Dev, identity.Ino, identity.Mode = true, stat.Dev, stat.Ino, stat.Mode&unix.S_IFMT
		pin.file, pin.parent = file, parent
	} else {
		leaf, relErr := filepath.Rel(parentPath, path)
		if relErr != nil || leaf == "." || strings.HasPrefix(leaf, "..") {
			_ = parent.Close()
			return Identity{}, nil, errors.New("invalid isolation leaf")
		}
		pin.leaf = leaf
	}
	pin.identity = identity
	got, err := Capture(path)
	if err != nil || got != identity {
		_ = pin.Close()
		return Identity{}, nil, errors.New("isolation object changed during capture")
	}
	return identity, pin, nil
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

// FromFiles takes ownership of descriptors received from the trusted broker.
// Existing objects carry an object and parent FD; missing leaves carry only
// their nearest existing parent FD.
func FromFiles(identities []Identity, files []*os.File) (*Set, error) {
	set := &Set{grants: make(map[string]*Pinned, len(identities))}
	closeUnowned := func(start int) {
		for _, file := range files[start:] {
			if file != nil {
				_ = file.Close()
			}
		}
	}
	index := 0
	for _, identity := range identities {
		count := 1
		if identity.Exists {
			count = 2
		}
		if index+count > len(files) {
			closeUnowned(index)
			_ = set.Close()
			return nil, errors.New("invalid received isolation inventory")
		}
		if _, exists := set.grants[identity.Path]; exists {
			closeUnowned(index)
			_ = set.Close()
			return nil, errors.New("duplicate received isolation object")
		}
		pin, err := receivedPin(identity, files[index:index+count])
		if err != nil {
			closeUnowned(index)
			_ = set.Close()
			return nil, err
		}
		set.grants[identity.Path] = pin
		index += count
	}
	if index != len(files) {
		closeUnowned(index)
		_ = set.Close()
		return nil, errors.New("extra received isolation descriptors")
	}
	if err := set.Validate(); err != nil {
		_ = set.Close()
		return nil, err
	}
	return set, nil
}

func receivedPin(identity Identity, files []*os.File) (*Pinned, error) {
	if err := validatePath(identity.Path); err != nil || files[0] == nil || (identity.Exists && files[1] == nil) {
		return nil, errors.New("invalid received isolation inventory")
	}
	pin := &Pinned{identity: identity, file: files[0]}
	if identity.Exists {
		pin.parent = files[1]
		return pin, nil
	}
	leaf, err := filepath.Rel(identity.ParentPath, identity.Path)
	if err != nil || leaf == "." || strings.HasPrefix(leaf, "..") {
		return nil, errors.New("invalid received isolation leaf")
	}
	pin.leaf = leaf
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
	if err := p.validateRetainedHandles(); err != nil {
		return err
	}
	got, err := Capture(p.identity.Path)
	if err != nil || got != p.identity {
		return errors.New("isolation object changed after pinning")
	}
	return nil
}

func (p *Pinned) validateRetainedHandles() error {
	if p.file == nil {
		return errors.New("isolation grant descriptor is missing")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(p.file.Fd()), &stat); err != nil {
		return fmt.Errorf("isolation grant descriptor is closed: %w", err)
	}
	if !p.identity.Exists {
		if stat.Dev != p.identity.ParentDev || stat.Ino != p.identity.ParentIno {
			return errors.New("isolation parent descriptor changed")
		}
		return nil
	}
	if stat.Dev != p.identity.Dev || stat.Ino != p.identity.Ino || stat.Mode&unix.S_IFMT != p.identity.Mode || p.parent == nil {
		return errors.New("isolation object descriptor changed")
	}
	if err := unix.Fstat(int(p.parent.Fd()), &stat); err != nil || stat.Dev != p.identity.ParentDev || stat.Ino != p.identity.ParentIno {
		return errors.New("isolation parent descriptor changed")
	}
	return nil
}
