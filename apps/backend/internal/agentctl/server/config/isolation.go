package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kandev/kandev/internal/agentctl/server/process/isolation"
)

// WorkspaceIsolation describes canonical paths granted to an agent instance.
type WorkspaceIsolation struct {
	SharedCommonDirs       []string             `json:"shared_common_dirs"`
	WritablePaths          []string             `json:"writable_paths"`
	PrivateAdminPaths      []string             `json:"private_admin_paths"`
	WritableWorkspaceRoots []string             `json:"writable_workspace_roots"`
	RequiredDirectories    []string             `json:"required_directories"`
	ObjectIdentities       []isolation.Identity `json:"object_identities"`
}

func (m WorkspaceIsolation) requiredDirectoryPaths() map[string]struct{} {
	paths := make(map[string]struct{})
	for _, group := range [][]string{m.SharedCommonDirs, m.PrivateAdminPaths, m.WritableWorkspaceRoots, m.RequiredDirectories} {
		for _, path := range group {
			paths[path] = struct{}{}
		}
	}
	return paths
}

func (m WorkspaceIsolation) grantPaths() map[string]struct{} {
	paths := make(map[string]struct{})
	for _, group := range [][]string{m.SharedCommonDirs, m.WritablePaths, m.PrivateAdminPaths, m.WritableWorkspaceRoots} {
		for _, path := range group {
			paths[path] = struct{}{}
		}
	}
	return paths
}

// CaptureObjects binds every manifest grant to its current filesystem object.
func (m *WorkspaceIsolation) CaptureObjects() error {
	if err := m.Validate(); err != nil {
		return err
	}
	m.ObjectIdentities = nil
	paths := m.grantPaths()
	required := m.requiredDirectoryPaths()
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		identity, err := isolation.Capture(path)
		if err != nil {
			m.ObjectIdentities = nil
			return fmt.Errorf("capture isolation object: %w", err)
		}
		if _, mustExist := required[path]; mustExist && !identity.IsDirectory() {
			m.ObjectIdentities = nil
			return fmt.Errorf("required isolation directory is missing or changed")
		}
		m.ObjectIdentities = append(m.ObjectIdentities, identity)
	}
	return nil
}

// ValidateObjects requires an exact object identity for every lexical grant.
func (m WorkspaceIsolation) ValidateObjects() error {
	if err := m.Validate(); err != nil {
		return err
	}
	paths := m.grantPaths()
	required := m.requiredDirectoryPaths()
	for path := range required {
		if _, ok := paths[path]; !ok {
			return fmt.Errorf("required isolation directory is not a grant")
		}
	}
	if len(paths) != len(m.ObjectIdentities) {
		return fmt.Errorf("isolation object inventory does not match grants")
	}
	for _, identity := range m.ObjectIdentities {
		if _, ok := paths[identity.Path]; !ok {
			return fmt.Errorf("isolation object inventory contains an unexpected path")
		}
		if _, mustExist := required[identity.Path]; mustExist && !identity.IsDirectory() {
			return fmt.Errorf("required isolation directory is missing or changed")
		}
		delete(paths, identity.Path)
	}
	if len(paths) != 0 {
		return fmt.Errorf("isolation object inventory is incomplete")
	}
	return nil
}

// ConfirmObjects detects a change while trusted projection validation runs.
func (m WorkspaceIsolation) ConfirmObjects() error {
	if err := m.ValidateObjects(); err != nil {
		return err
	}
	for _, want := range m.ObjectIdentities {
		got, err := isolation.Capture(want.Path)
		if err != nil || got != want {
			return fmt.Errorf("isolation object changed during projection validation")
		}
	}
	return nil
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
	if len(m.SharedCommonDirs) == 0 || len(m.WritablePaths) == 0 || len(m.WritableWorkspaceRoots) == 0 {
		return fmt.Errorf("workspace isolation has no shared or writable paths")
	}
	for _, shared := range m.SharedCommonDirs {
		if shared == string(filepath.Separator) {
			return fmt.Errorf("shared common directory cannot be a filesystem root")
		}
	}
	for _, paths := range [][]string{m.SharedCommonDirs, m.WritablePaths, m.PrivateAdminPaths, m.WritableWorkspaceRoots, m.RequiredDirectories} {
		for _, path := range paths {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
				return fmt.Errorf("workspace isolation path is not canonical")
			}
		}
	}
	for _, root := range m.WritableWorkspaceRoots {
		for _, shared := range m.SharedCommonDirs {
			if overlapsSharedCommon(shared, root) {
				return fmt.Errorf("writable workspace root overlaps shared common directory")
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
	if writable == string(filepath.Separator) {
		return true
	}
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
