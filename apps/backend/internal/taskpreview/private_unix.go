//go:build !windows

package taskpreview

import (
	"os"
	"syscall"
)

func privateDirectory(path string) error {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0077 != 0 {
		return ErrDenied
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) {
		return ErrDenied
	}
	return nil
}
func privateFile(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrDenied
	}
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0 {
		_ = f.Close()
		return nil, ErrDenied
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) {
		_ = f.Close()
		return nil, ErrDenied
	}
	return f, nil
}
