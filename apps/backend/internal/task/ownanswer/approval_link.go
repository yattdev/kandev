package ownanswer

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/task/models"
)

const ApprovalLinkPurpose = "new-authorization/historical-linkage-unavailable/v1"

// ApprovalLinkCase is deliberately closed to the one reviewed repair. Hashes
// identify stored evidence; they never establish the administrator's authority.
func ApprovalLinkCase() OwnAnswerBinding {
	return OwnAnswerBinding{
		WorkspaceID: "2e62401b-5ffe-4050-bc1b-d49ea5d5dbcd",
		TaskID:      "a68df3ae-aaf5-4591-a46d-9d73db62e46d", SessionID: "1a1e64ab-926b-4c9a-a862-967d815b8779",
		QuestionID: "preview_trusted_model_validation_20261015", PendingID: "ecadc06d-bd52-456a-96bc-09e75493e4df",
		QuestionMessageID: "89e5496c-cf37-4746-9253-52dbc0d8e991", QuestionSHA256: "d02f816db3fd1ceb85e1a18da6b272c10dd15bd9883ea54798a45ed8ca25f44d",
		AnswerMessageID: "8ef1ea88-3d79-44ca-a84c-6da258063195", AnswerSHA256: "8f9dc25bb64576d42515886702df42fa76de83bd73aafe4edbf0bd28355b0f1e",
	}
}

type ApprovalLinkRequest struct {
	OwnAnswerBinding
	OptionID string `json:"option_id"`
}

type ApprovalLinkGrant struct {
	ID      string `json:"grant_id"`
	Purpose string `json:"purpose"`
	ApprovalLinkRequest
	Workspace              string    `json:"workspace_id"`
	Incarnation            string    `json:"session_incarnation_id"`
	CreatorUserID          string    `json:"creator_user_id"`
	CreatorSessionID       string    `json:"creator_session_id"`
	CreatorOrgID           string    `json:"creator_org_id"`
	CreatorAuthoritySHA256 string    `json:"creator_authority_sha256"`
	IssuedAt               time.Time `json:"issued_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	QuestionEvidenceSHA256 string    `json:"question_evidence_sha256"`
	AnswerEvidenceSHA256   string    `json:"answer_evidence_sha256"`
	OriginalReceiptSHA256  string    `json:"original_receipt_sha256"`
	QuestionUpdatedAt      time.Time `json:"question_updated_at"`
	AnswerUpdatedAt        time.Time `json:"answer_updated_at"`
}

// BrowserGrantCreator rejects synthetic and agent-owner identities even when
// they carry an administrator role. Request fields cannot populate this context.
func BrowserGrantCreator(ctx context.Context) (authn.Identity, error) {
	id, ok := authn.IdentityFromContext(ctx)
	if !ok || id.Synthetic || !id.IsAdmin() || id.UserID == "" || id.SessionID == "" || id.TokenID != "" || id.Instance {
		return authn.Identity{}, errors.New("real workspace administrator browser session required")
	}
	return id, nil
}

// ApprovalEvidenceDigest covers complete bodies and metadata, including context
// and all option descriptions. Only the known settlement fields are normalized
// for idempotent readback; every other stored byte remains bound.
func ApprovalEvidenceDigest(m *models.Message, originalUpdatedAt time.Time, settled bool) (string, error) {
	copy := *m
	copy.Metadata = make(map[string]any, len(m.Metadata))
	for k, v := range m.Metadata {
		copy.Metadata[k] = v
	}
	if settled {
		delete(copy.Metadata, OwnAnswerBindingKey)
		if m.Type == models.MessageTypeClarificationRequest {
			delete(copy.Metadata, OwnAnswerReceiptKey)
			delete(copy.Metadata, "response")
			copy.Metadata["status"] = "pending"
		}
		copy.UpdatedAt = originalUpdatedAt
	}
	raw, err := json.Marshal(copy)
	return OwnAnswerDigest(raw), err
}
