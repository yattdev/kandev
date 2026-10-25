package manifest

import "fmt"

// MinimumMessagesCapabilityVersion is the first Host release that supports
// both Go Messages().List and the browser conversation facade safely.
const MinimumMessagesCapabilityVersion = "0.91.1"

// RequiresMessagesCapabilityMinimum reports whether a manifest opts into the
// messages read resource whose Host contract requires a minimum release.
func RequiresMessagesCapabilityMinimum(manifest *Manifest) bool {
	return manifest != nil && manifest.Capabilities.CanRead("messages")
}

func (m *Manifest) validateCapabilityMinimumVersions() []error {
	var errs []error
	if RequiresMessagesCapabilityMinimum(m) {
		minimum, valid := NormalizeReleaseVersion(m.MinKandevVersion)
		if !valid || CompareVersions(minimum, MinimumMessagesCapabilityVersion) < 0 {
			errs = append(errs, fmt.Errorf(
				"api_read resource %q requires min_kandev_version >= %s",
				"messages",
				MinimumMessagesCapabilityVersion,
			))
		}
	}
	if RequiresExactHostCapabilityMinimum(m) {
		if _, valid := NormalizeReleaseVersion(m.MinKandevVersion); !valid {
			errs = append(errs, fmt.Errorf("exact Host capabilities require a valid min_kandev_version"))
		}
	}
	return errs
}

// RequiresExactHostCapabilityMinimum reports whether a manifest opts into
// approval-bound Host v2 capabilities, which must never be installed against
// an unversioned Host contract.
func RequiresExactHostCapabilityMinimum(manifest *Manifest) bool {
	return manifest != nil && (len(manifest.Capabilities.HostV2Read) > 0 || len(manifest.Capabilities.HostV2Write) > 0)
}
