package provideraccess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

var ErrTargetInvalid = errors.New("provider access target identity invalid")

// GitHubRerunTarget is the immutable provider identity captured by the Host.
// The plugin cannot choose or change this identity when redeeming a lease.
type GitHubRerunTarget struct {
	Operation        string
	PRNumber         int
	BaseRepositoryID int64
	BaseRepository   string
	BaseRef          string
	BaseSHA          string
	HeadRepositoryID int64
	HeadRepository   string
	HeadRef          string
	HeadSHA          string
	SourceRunID      int64
	SourceAttempt    int
	WorkflowID       int64
}

// Digest binds every target selector into the durable lease without storing
// a caller-provided mutable ref as authority.
func (target GitHubRerunTarget) Digest() (string, error) {
	if target.Operation != "rerun_failed_jobs" || target.PRNumber <= 0 ||
		target.BaseRepositoryID <= 0 || !validCanonicalGitHubRepository(target.BaseRepository) ||
		target.BaseRef == "" || !validGitHubSHA(target.BaseSHA) ||
		target.HeadRepositoryID <= 0 || !validCanonicalGitHubRepository(target.HeadRepository) ||
		target.HeadRef == "" || !validGitHubSHA(target.HeadSHA) ||
		target.SourceRunID <= 0 || target.SourceAttempt <= 0 || target.WorkflowID <= 0 {
		return "", ErrTargetInvalid
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validGitHubSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	return strings.IndexFunc(value, func(ch rune) bool {
		return (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f')
	}) == -1
}
