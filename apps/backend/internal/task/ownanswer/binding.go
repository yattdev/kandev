package ownanswer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/task/models"
	"strings"
	"time"
)

const OwnAnswerReceiptKey = "own_question_answer_receipt"
const OwnAnswerBindingKey = "own_question_answer_binding"

// DispositionMode is injected by updated MCP clients, never tool arguments.
// Older clients keep receiving errors rather than a refusal they might mistake
// for successful answer reconciliation.
const DispositionMode = "explicit_disposition_v1"

// ErrAnswerFormatUnsupported never implies permission to select an option or
// retire the original question. Repositories expose it only after provenance,
// disconnected/unclaimed state and ambiguity checks have also passed.
var ErrAnswerFormatUnsupported = errors.New("ANSWER_FORMAT_UNSUPPORTED")

// OwnAnswerBinding names persisted evidence, never a caller-supplied answer.
type OwnAnswerBinding struct {
	TaskID            string `json:"task_id"`
	SessionID         string `json:"session_id"`
	WorkspaceID       string `json:"-"`
	QuestionID        string `json:"question_id"`
	PendingID         string `json:"pending_id"`
	QuestionMessageID string `json:"question_message_id"`
	QuestionSHA256    string `json:"question_sha256"`
	AnswerMessageID   string `json:"answer_message_id"`
	AnswerSHA256      string `json:"answer_sha256"`
}

type OwnAnswerReceipt struct {
	ApprovalGrantID      string `json:"approval_grant_id,omitempty"`
	ApprovalOperationID  string `json:"approval_operation_id,omitempty"`
	Version              string `json:"version"`
	ID                   string `json:"receipt_id"`
	Workspace            string `json:"workspace_id"`
	SessionIncarnationID string `json:"session_incarnation_id"`
	AdmissionID          string `json:"admission_id"`
	AdmissionSHA256      string `json:"admission_sha256"`
	OwnAnswerBinding
	AnswerMetadataSHA256 string    `json:"answer_metadata_sha256"`
	AnswerCreatedAt      time.Time `json:"answer_created_at"`
	BoundAt              time.Time `json:"bound_at"`
	Provenance           string    `json:"provenance"`
}

func OwnAnswerDigest(data []byte) string {
	d := sha256.Sum256(data)
	return hex.EncodeToString(d[:])
}

func (b OwnAnswerBinding) Validate() error {
	for _, id := range []string{b.TaskID, b.SessionID, b.QuestionID, b.PendingID, b.QuestionMessageID, b.AnswerMessageID} {
		if id == "" || len(id) > 128 {
			return errors.New("exact bounded identities required")
		}
	}
	for _, hash := range []string{b.QuestionSHA256, b.AnswerSHA256} {
		bytes, err := hex.DecodeString(hash)
		if err != nil || len(bytes) != 32 || strings.ToLower(hash) != hash {
			return errors.New("exact lowercase SHA256 required")
		}
	}
	return nil
}

// Native ingress cannot accept arbitrary metadata. MCP task messages always
// carry sender/intent metadata; unknown provenance is deliberately held.
func nativeHumanIngress(m *models.Message) bool {
	if m.AuthorType != models.MessageAuthorUser || m.Type != models.MessageTypeMessage || m.RequestsInput || m.TurnID == "" {
		return false
	}
	for k := range m.Metadata {
		if k != "client_message_request_fingerprint" && k != "queue_admission_ids" && k != OwnAnswerBindingKey {
			return false
		}
	}
	fp, _ := m.Metadata["client_message_request_fingerprint"].(string)
	bytes, err := hex.DecodeString(fp)
	if err != nil || len(bytes) != 32 {
		return false
	}
	ids, err := json.Marshal(m.Metadata["queue_admission_ids"])
	if err != nil {
		return false
	}
	var admissions []string
	if json.Unmarshal(ids, &admissions) != nil || len(admissions) != 1 {
		return false
	}
	_, err = uuid.Parse(admissions[0])
	return err == nil
}

func OwnQuestionEnvelope(m *models.Message) (Question, string, error) {
	raw, err := json.Marshal(m.Metadata["question"])
	if err != nil || len(raw) > 65536 {
		return Question{}, "", errors.New("question exceeds bound")
	}
	var q Question
	if json.Unmarshal(raw, &q) != nil || q.ID == "" || len(q.Options) < 2 || len(q.Options) > 6 {
		return q, "", errors.New("invalid question")
	}
	contextText, _ := m.Metadata["context"].(string)
	return q, contextText, nil
}

func exactHumanOption(m *models.Message, q Question, contextText string) (string, error) {
	prefix := ""
	if contextText != "" {
		prefix = "Context: " + contextText + "\n\n"
	}
	prefix += fmt.Sprintf("Question 1: %s\n%s\nAnswer: ", q.Title, q.Prompt)
	selected := ""
	for _, o := range q.Options {
		if m.Content == prefix+o.Label {
			if selected != "" || o.ID == "" {
				return "", errors.New("ambiguous option")
			}
			selected = o.ID
		}
	}
	if selected == "" {
		return "", ErrAnswerFormatUnsupported
	}
	return selected, nil
}

// CorrelateOwnAnswer is closed-format validation, not natural-language inference.
func CorrelateOwnAnswer(b OwnAnswerBinding, qm, am *models.Message, at time.Time) (*Answer, *OwnAnswerReceipt, error) {
	if err := validateOwnAnswerEvidence(b, qm, am); err != nil {
		return nil, nil, err
	}
	q, contextText, err := OwnQuestionEnvelope(qm)
	if err != nil || q.ID != b.QuestionID {
		return nil, nil, errors.New("question envelope unavailable")
	}
	option, err := exactHumanOption(am, q, contextText)
	if err != nil {
		return nil, nil, err
	}
	meta := make(map[string]any, len(am.Metadata))
	for k, v := range am.Metadata {
		if k != OwnAnswerBindingKey {
			meta[k] = v
		}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, nil, err
	}
	identity, _ := json.Marshal(b)
	receipt := &OwnAnswerReceipt{Version: "own-question-answer/v1", ID: uuid.NewSHA1(uuid.NameSpaceOID, identity).String(), OwnAnswerBinding: b, AnswerMetadataSHA256: OwnAnswerDigest(raw), AnswerCreatedAt: am.CreatedAt, BoundAt: at, Provenance: "server-native-user-ingress/exact-single-question-envelope-v1"}
	return &Answer{QuestionID: q.ID, SelectedOptions: []string{option}}, receipt, nil
}

// Closed native single-choice envelope; deliberately excludes free-text inference.
type Option struct {
	ID          string `json:"option_id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}
type Question struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Prompt  string   `json:"prompt"`
	Options []Option `json:"options"`
}
type Answer struct {
	QuestionID      string   `json:"question_id"`
	SelectedOptions []string `json:"selected_options,omitempty"`
	CustomText      string   `json:"custom_text,omitempty"`
}

func validateOwnAnswerEvidence(b OwnAnswerBinding, qm, am *models.Message) error {
	if err := b.Validate(); err != nil {
		return err
	}
	for _, m := range []*models.Message{qm, am} {
		if m == nil || m.TaskID != b.TaskID || m.TaskSessionID != b.SessionID {
			return errors.New("answer evidence scope differs")
		}
	}
	if err := validateOwnQuestionIdentity(b, qm, am); err != nil {
		return err
	}
	raw, err := json.Marshal(qm.Metadata["question"])
	if err != nil || OwnAnswerDigest(raw) != b.QuestionSHA256 || OwnAnswerDigest([]byte(am.Content)) != b.AnswerSHA256 {
		return errors.New("answer evidence digest differs")
	}
	if !nativeHumanIngress(am) || !am.CreatedAt.After(qm.CreatedAt) || !am.UpdatedAt.Equal(am.CreatedAt) && am.Metadata[OwnAnswerBindingKey] == nil {
		return errors.New("native Human ingress provenance unavailable")
	}
	return nil
}

func validateOwnQuestionIdentity(b OwnAnswerBinding, qm, am *models.Message) error {
	if qm.ID != b.QuestionMessageID || am.ID != b.AnswerMessageID || qm.Type != models.MessageTypeClarificationRequest || qm.AuthorType != models.MessageAuthorAgent || !qm.RequestsInput || qm.Metadata["pending_id"] != b.PendingID || qm.Metadata["question_id"] != b.QuestionID {
		return errors.New("question identity differs")
	}
	return nil
}
