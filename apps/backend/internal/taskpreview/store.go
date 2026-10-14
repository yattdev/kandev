package taskpreview

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// Store is private to the host service and outer backend/broker. The agent
// guard replaces /run, hiding the container's /run/kandev-task-preview mount.
type Store struct{ Directory string }

func (s Store) trustedDirectory() error { return privateDirectory(s.Directory) }
func (s Store) read(name string, limit int64) ([]byte, error) {
	if s.trustedDirectory() != nil {
		return nil, ErrDenied
	}
	f, err := privateFile(filepath.Join(s.Directory, name))
	if err != nil {
		return nil, ErrDenied
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.Size() > limit {
		return nil, ErrDenied
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}
func (s Store) Load() (Grant, []byte, error) {
	var g Grant
	b, err := s.read("manifest.json", 16384)
	if err != nil || json.Unmarshal(b, &g) != nil || g.Validate() != nil {
		return g, nil, ErrDenied
	}
	key, err := s.read("key", 32)
	if err != nil || len(key) != 32 || s.Revoked() {
		return g, nil, ErrDenied
	}
	return g, key, nil
}
func (s Store) Revoked() bool {
	_, err := os.Lstat(filepath.Join(s.Directory, "revoked"))
	return err == nil || !os.IsNotExist(err)
}
func (s Store) exclusive(name string, body []byte) error {
	if s.trustedDirectory() != nil {
		return ErrDenied
	}
	f, err := os.OpenFile(filepath.Join(s.Directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrDenied
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(body); err != nil {
		return err
	}
	return f.Sync()
}
func (s Store) Revoke() error {
	if s.Revoked() {
		return nil
	}
	return s.exclusive("revoked", []byte("revoked\n"))
}
func (s Store) Redeem(c Claims) error {
	// The gateway can write only this child. It cannot change grant authority,
	// login delivery or the irreversible revocation marker.
	nonces := Store{Directory: filepath.Join(s.Directory, "nonces")}
	return nonces.exclusive(c.Nonce, []byte("used\n"))
}
