package manifest

import (
	"fmt"
	"strings"
)

// HasEvent reports whether the manifest's declared event subscriptions
// (Capabilities.Events) cover the given concrete event name, including
// wildcard subscriptions such as "task.*".
func (m *Manifest) HasEvent(name string) bool {
	for _, pattern := range m.Capabilities.Events {
		if MatchSubject(pattern, name) {
			return true
		}
	}
	return false
}

// CanRead reports whether the manifest declares read access to resource via
// Capabilities.APIRead.
func (m *Manifest) CanRead(resource string) bool {
	return m.Capabilities.CanRead(resource)
}

// CanWrite reports whether the manifest declares write access to resource
// via Capabilities.APIWrite.
func (m *Manifest) CanWrite(resource string) bool {
	return m.Capabilities.CanWrite(resource)
}

// CanRead reports whether c declares read access to resource via APIRead
// (ADR 0043's api_read:<resource> capabilities, e.g. "tasks", "sessions").
// Exposed on Capabilities directly (not just Manifest) so callers that only
// hold a plugin's currently-registered Capabilities snapshot — such as
// internal/plugins.pluginHost, bound at spawn time — can gate without a full
// Manifest.
func (c Capabilities) CanRead(resource string) bool {
	return containsString(c.APIRead, resource)
}

// CanWrite reports whether c declares write access to resource via
// APIWrite. See CanRead's doc comment for why this also lives on
// Capabilities directly.
func (c Capabilities) CanWrite(resource string) bool {
	return containsString(c.APIWrite, resource)
}

// CanReadExact reports whether c declares an approval-bound exact Host read.
// It never consults the legacy APIRead declaration.
func (c Capabilities) CanReadExact(resource string) bool {
	return containsString(c.HostV2Read, resource)
}

// CanWriteExact reports whether c declares an approval-bound exact Host write.
// It never consults the legacy APIWrite declaration.
func (c Capabilities) CanWriteExact(resource string) bool {
	return containsString(c.HostV2Write, resource)
}

func (m *Manifest) validateExactHostV2Capabilities() []error {
	var errs []error
	errs = append(errs, validateExactCapabilityResources("host_v2_read", m.Capabilities.HostV2Read)...)
	errs = append(errs, validateExactCapabilityResources("host_v2_write", m.Capabilities.HostV2Write)...)
	return errs
}

func validateExactCapabilityResources(name string, resources []string) []error {
	seen := make(map[string]struct{}, len(resources))
	var errs []error
	for index, resource := range resources {
		if !isExactCapabilityResource(resource) {
			errs = append(errs, fmt.Errorf("%s[%d] must be a lowercase exact resource", name, index))
			continue
		}
		if isHumanReservedExactResource(resource) {
			errs = append(errs, fmt.Errorf("%s[%d] declares Human-reserved resource %q", name, index, resource))
			continue
		}
		if _, duplicate := seen[resource]; duplicate {
			errs = append(errs, fmt.Errorf("%s[%d] duplicates %q", name, index, resource))
			continue
		}
		seen[resource] = struct{}{}
	}
	return errs
}

func isExactCapabilityResource(resource string) bool {
	if resource == "" || strings.TrimSpace(resource) != resource {
		return false
	}
	for _, character := range resource {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func isHumanReservedExactResource(resource string) bool {
	switch resource {
	case "merge", "deploy", "release", "rewrite_history", "cross_workspace", "secret_scope_expand":
		return true
	default:
		return false
	}
}

// HasUIBundle reports whether the manifest declares a native UI bundle via
// UISection.Bundle.
func (m *Manifest) HasUIBundle() bool {
	return m.UI.Bundle != ""
}

// containsString reports whether target is present in values.
func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
