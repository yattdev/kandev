package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// WorkspaceIsolation describes canonical paths granted to an agent instance.
type WorkspaceIsolation struct {
	SharedCommonDirs  []string `json:"shared_common_dirs"`
	WritablePaths     []string `json:"writable_paths"`
	PrivateAdminPaths []string `json:"private_admin_paths"`
}

// Validate rejects writable grants that overlap a shared Git common directory.
func (m WorkspaceIsolation) Validate() error {
	if err := m.validatePaths(); err != nil {
		return err
	}
	for _, private := range m.PrivateAdminPaths {
		if !m.isPrivateAdminPath(private) {
			return fmt.Errorf("private administration path is not a linked worktree")
		}
	}
	for _, shared := range m.SharedCommonDirs {
		for _, writable := range m.WritablePaths {
			if overlapsSharedCommon(shared, writable) && !containedByPrivateAdmin(m.PrivateAdminPaths, writable) {
				return fmt.Errorf("writable path overlaps shared common directory")
			}
		}
	}
	return nil
}

func (m WorkspaceIsolation) validatePaths() error {
	if len(m.SharedCommonDirs) == 0 || len(m.WritablePaths) == 0 {
		return fmt.Errorf("workspace isolation has no shared or writable paths")
	}
	for _, paths := range [][]string{m.SharedCommonDirs, m.WritablePaths, m.PrivateAdminPaths} {
		for _, path := range paths {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return fmt.Errorf("workspace isolation path is not canonical")
			}
		}
	}
	return nil
}

func (m WorkspaceIsolation) isPrivateAdminPath(private string) bool {
	for _, shared := range m.SharedCommonDirs {
		if filepath.Dir(private) == filepath.Join(shared, "worktrees") {
			return true
		}
	}
	return false
}

func overlapsSharedCommon(shared, writable string) bool {
	return writable == shared || strings.HasPrefix(writable, shared+string(filepath.Separator)) ||
		strings.HasPrefix(shared, writable+string(filepath.Separator))
}

func containedByPrivateAdmin(paths []string, want string) bool {
	for _, path := range paths {
		if path == want || strings.HasPrefix(want, path+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
