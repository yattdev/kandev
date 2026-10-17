package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/authz"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/ownanswer"
)

type approvalLinkCreator interface {
	CreateApprovalLinkGrant(context.Context, ownanswer.ApprovalLinkRequest) (*ownanswer.ApprovalLinkGrant, error)
}

type approvalLinkRevoker interface {
	RevokeApprovalLinkGrant(context.Context, string) error
}

func approvalBrowserAuthorized(c *gin.Context) bool {
	_, err := ownanswer.BrowserGrantCreator(c.Request.Context())
	_, taskCaller := mcpscope.PrincipalFromContext(c.Request.Context())
	return err == nil && !taskCaller
}

type approvalLinkCreation struct {
	ownanswer.ApprovalLinkRequest
	Purpose string `json:"purpose"`
}

func decodeApprovalLinkCreation(c *gin.Context) (approvalLinkCreation, error) {
	var body approvalLinkCreation
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8192))
	if err != nil {
		return body, err
	}
	keys := json.NewDecoder(bytes.NewReader(raw))
	start, err := keys.Token()
	if err != nil || start != json.Delim('{') {
		return body, errors.New("expected one object")
	}
	seen := map[string]bool{}
	for keys.More() {
		key, err := keys.Token()
		if err != nil {
			return body, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return body, errors.New("ambiguous request key")
		}
		seen[name] = true
		var value string
		if err := keys.Decode(&value); err != nil {
			return body, err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return body, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return body, errors.New("expected one request")
	}
	return body, nil
}

// Grant issuance requires a browser-authenticated administrator. Task-owner
// identities and the auth-disabled synthetic administrator confer no authority.
func (h *TaskHandlers) httpCreateApprovalLinkGrant(c *gin.Context) {
	h.createApprovalLinkGrant(c, ownanswer.ApprovalLinkCase())
}

func (h *TaskHandlers) createApprovalLinkGrant(c *gin.Context, expected ownanswer.OwnAnswerBinding) {
	if !approvalBrowserAuthorized(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "authenticated workspace administrator browser session required"})
		return
	}
	body, err := decodeApprovalLinkCreation(c)
	if err != nil || body.Purpose != ownanswer.ApprovalLinkPurpose {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exact new-authorization request required"})
		return
	}
	body.WorkspaceID = expected.WorkspaceID
	if body.OwnAnswerBinding != expected || c.Param("id") != body.TaskID {
		c.JSON(http.StatusForbidden, gin.H{"error": "reviewed approval-link case required"})
		return
	}
	if h.service == nil || h.service.AuthorizeTaskScope(c.Request.Context(), body.TaskID, authz.ScopeTaskWrite) != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "workspace task-write authorization required"})
		return
	}
	creator, available := h.repo.(approvalLinkCreator)
	if !available {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "approval-link issuance unavailable"})
		return
	}
	grant, err := creator.CreateApprovalLinkGrant(c.Request.Context(), body.ApprovalLinkRequest)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "approval-link evidence unavailable or already claimed"})
		return
	}
	c.JSON(http.StatusCreated, grant)
}

func (h *TaskHandlers) httpRevokeApprovalLinkGrant(c *gin.Context) {
	if !approvalBrowserAuthorized(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "authenticated workspace administrator browser session required"})
		return
	}
	b := ownanswer.ApprovalLinkCase()
	if c.Param("id") != b.TaskID || h.service == nil || h.service.AuthorizeTaskScope(c.Request.Context(), b.TaskID, authz.ScopeTaskWrite) != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "reviewed case and workspace task-write authorization required"})
		return
	}
	revoker, ok := h.repo.(approvalLinkRevoker)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "approval-link revocation unavailable"})
		return
	}
	if err := revoker.RevokeApprovalLinkGrant(c.Request.Context(), c.Param("grant_id")); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "unconsumed creator-owned approval-link grant required"})
		return
	}
	c.Status(http.StatusNoContent)
}
