package executor

// Attach configuration must describe the existing checkout, not materialize
// today's PR target. Copy the launch-local values so fresh launches and
// comparison consumers retain the verified live PR identity.
func repoInfosForWorkspaceReuse(infos []*repoInfo) []*repoInfo {
	result := make([]*repoInfo, len(infos))
	for i, info := range infos {
		if info == nil {
			continue
		}
		copy := *info
		copy.BaseBranch = info.WorkspaceBaseBranch
		copy.QualifiedPRBase = nil
		copy.RemoteContribution = nil
		result[i] = &copy
	}
	return result
}
