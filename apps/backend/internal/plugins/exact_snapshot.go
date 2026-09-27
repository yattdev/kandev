package plugins

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

const exactSnapshotSecretBytes = 32
const exactSnapshotCursorTTL = 5 * time.Minute

var errExactSnapshotCursor = errors.New("plugins: exact snapshot cursor is invalid")

type exactSnapshotBinding struct {
	InstallationID    string `json:"installation_id"`
	WorkspaceID       string `json:"workspace_id"`
	FilterDigest      string `json:"filter_digest"`
	ApprovalRevision  uint64 `json:"approval_revision"`
	ProjectionVersion string `json:"projection_version"`
}

type exactSnapshotCursor struct {
	exactSnapshotBinding
	Offset            int   `json:"offset"`
	ExpiresAtUnixNano int64 `json:"expires_at_unix_nano"`
}
type exactSnapshotStore struct{ secret []byte }

func newExactSnapshotStore(secret []byte) *exactSnapshotStore {
	return &exactSnapshotStore{secret: append([]byte(nil), secret...)}
}

// newConnectionExactSnapshotStore creates a secret that belongs only to one
// broker-bound Host instance. Installation IDs are visible to the plugin and
// therefore can never authenticate a cursor.
func newConnectionExactSnapshotStore() (*exactSnapshotStore, error) {
	secret := make([]byte, exactSnapshotSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return newExactSnapshotStore(secret), nil
}

func (s *exactSnapshotStore) create(binding exactSnapshotBinding, offset int) (string, error) {
	if offset < 0 || binding.InstallationID == "" || binding.WorkspaceID == "" || binding.FilterDigest == "" || binding.ApprovalRevision == 0 || binding.ProjectionVersion == "" {
		return "", errExactSnapshotCursor
	}
	payload, err := json.Marshal(exactSnapshotCursor{exactSnapshotBinding: binding, Offset: offset, ExpiresAtUnixNano: time.Now().Add(exactSnapshotCursorTTL).UnixNano()})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...)), nil
}

func (s *exactSnapshotStore) offset(cursor string, want exactSnapshotBinding) (int, error) {
	got, err := s.parse(cursor)
	if err != nil || got.exactSnapshotBinding != want {
		return 0, errExactSnapshotCursor
	}
	return got.Offset, nil
}

func (s *exactSnapshotStore) parse(cursor string) (exactSnapshotCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) <= sha256.Size {
		return exactSnapshotCursor{}, errExactSnapshotCursor
	}
	payload, signature := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return exactSnapshotCursor{}, errExactSnapshotCursor
	}
	var got exactSnapshotCursor
	if json.Unmarshal(payload, &got) != nil || got.Offset < 0 || got.ExpiresAtUnixNano <= time.Now().UnixNano() {
		return exactSnapshotCursor{}, errExactSnapshotCursor
	}
	return got, nil
}
