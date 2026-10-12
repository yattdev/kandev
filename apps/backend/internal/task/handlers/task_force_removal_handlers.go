package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/task/service"
)

type httpForceRemovalPreviewRequest struct {
	WorkspaceID            string `json:"workspace_id"`
	ExpectedTaskGeneration string `json:"expected_task_generation"`
}

type httpForceRemovalCommitRequest struct {
	ConfirmationTaskID     string `json:"confirmation_task_id"`
	WorkspaceID            string `json:"workspace_id"`
	ExpectedTaskGeneration string `json:"expected_task_generation"`
	AdmissionGeneration    string `json:"admission_generation"`
	OperationID            string `json:"operation_id"`
	RequestDigest          string `json:"request_digest"`
	PreviewDigest          string `json:"preview_digest"`
}

// httpPreviewForceRemoval only issues the read-only snapshot token. Commit,
// grant, and cleanup entry points remain unavailable until their fences exist.
func (h *TaskHandlers) httpPreviewForceRemoval(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if _, ok := authn.IdentityFromContext(c.Request.Context()); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	var body httpForceRemovalPreviewRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	if body.WorkspaceID == "" || body.ExpectedTaskGeneration == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id and expected_task_generation are required"})
		return
	}
	preview, err := h.service.PreviewForceRemoval(c.Request.Context(), c.Param("id"), body.WorkspaceID, body.ExpectedTaskGeneration)
	if err != nil {
		if errors.Is(err, service.ErrForceRemovalAdmissionStale) {
			c.JSON(http.StatusConflict, gin.H{"error": "force removal preview blocked"})
			return
		}
		handleNotFound(c, h.logger, err, "task not found")
		return
	}
	c.JSON(http.StatusOK, preview)
}

// httpCommitForceRemoval admits an exact human-authorized task only. Agent
// caller identity and grants are accepted solely on the MCP transport.
func (h *TaskHandlers) httpCommitForceRemoval(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if _, ok := authn.IdentityFromContext(c.Request.Context()); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	var body httpForceRemovalCommitRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	if body.ConfirmationTaskID != c.Param("id") || body.WorkspaceID == "" || body.ExpectedTaskGeneration == "" ||
		body.AdmissionGeneration == "" || body.OperationID == "" ||
		body.RequestDigest == "" || body.PreviewDigest == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "force removal confirmation and admission fields are required"})
		return
	}
	claim, replay, err := h.service.AdmitForceRemoval(c.Request.Context(), service.ForceRemovalAdmissionRequest{
		TaskID:                 c.Param("id"),
		WorkspaceID:            body.WorkspaceID,
		ExpectedTaskGeneration: body.ExpectedTaskGeneration,
		AdmissionGeneration:    body.AdmissionGeneration,
		OperationID:            body.OperationID,
		RequestDigest:          body.RequestDigest,
		PreviewDigest:          body.PreviewDigest,
	})
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "force removal admission denied"})
			return
		}
		if errors.Is(err, service.ErrForceRemovalAdmissionStale) || errors.Is(err, service.ErrForceRemovalPreviewInvalid) {
			c.JSON(http.StatusConflict, gin.H{"error": "force removal admission blocked"})
			return
		}
		handleNotFound(c, h.logger, err, "task not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": claim.TaskID, "operation_id": claim.OperationID, "replay": replay})
}
