package github

import "strings"

// MatchesAssociationEvidence compares the fields actually verified by an
// association request. A concurrent explicit replacement must not be reported
// as that request's successful resolution, even if it occupies the same slot.
func (p *TaskPR) MatchesAssociationEvidence(expected *TaskPR) bool {
	if p == nil || expected == nil || p.DetachedAt != nil {
		return false
	}
	return p.ID == expected.ID && p.TaskID == expected.TaskID && p.RepositoryID == expected.RepositoryID && p.PRNumber == expected.PRNumber &&
		strings.EqualFold(p.Owner, expected.Owner) && strings.EqualFold(p.Repo, expected.Repo) &&
		strings.EqualFold(strings.TrimSuffix(p.PRURL, "/"), strings.TrimSuffix(expected.PRURL, "/")) && strings.EqualFold(p.HeadSHA, expected.HeadSHA)
}
