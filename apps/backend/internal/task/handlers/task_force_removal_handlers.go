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
