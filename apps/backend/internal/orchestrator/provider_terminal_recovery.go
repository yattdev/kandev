package orchestrator

import (
	"context"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/task/models"
)

type pendingProviderTerminalClaims interface {
	ListPendingProviderAccessTerminalClaims(context.Context) ([]models.TerminalProviderAccessRecovery, error)
}

// reconcileProviderAccessTerminalClaimsOnStartup settles reservations left by
// an interrupted terminal transition. A failed or uncertain provider revoke
// retains the durable fence for a later operator-initiated restart after the
// provider's exposure ledger confirms revocation or token expiry.
func (s *Service) reconcileProviderAccessTerminalClaimsOnStartup(ctx context.Context) {
	reader, ok := s.repo.(pendingProviderTerminalClaims)
	if !ok {
		return
	}
	claims, err := reader.ListPendingProviderAccessTerminalClaims(ctx)
	if err != nil {
		s.logger.Error("failed to list pending provider terminal claims", zap.Error(err))
		return
	}
	for _, claim := range claims {
		s.reconcileProviderAccessTerminalClaim(ctx, claim)
	}
}

func (s *Service) reconcileProviderAccessTerminalClaim(ctx context.Context, claim models.TerminalProviderAccessRecovery) {
	if claim.ClaimID == "" || claim.ExpectedState == "" || !isTerminalSessionState(claim.TargetState) ||
		s.providerAccessSessionRevoker == nil {
		s.logger.Warn("provider terminal claim requires operator reconciliation",
			zap.String("session_id", claim.SessionID))
		return
	}
	if err := s.providerAccessSessionRevoker.RevokeSession(ctx, claim.SessionID); err != nil {
		s.logger.Warn("provider terminal claim remains fenced after uncertain revocation",
			zap.String("session_id", claim.SessionID), zap.Error(err))
		return
	}
	updater, ok := s.repo.(terminalClaimStateUpdater)
	if !ok {
		return
	}
	changed, updatedAt, err := updater.UpdateTaskSessionStateIfCurrentClaim(ctx,
		claim.SessionID, claim.ExpectedState, claim.TargetState, claim.ErrorMessage, claim.ClaimID)
	if err != nil {
		s.logger.Error("failed to commit recovered provider terminal claim",
			zap.String("session_id", claim.SessionID), zap.Error(err))
		return
	}
	session, err := s.repo.GetTaskSession(ctx, claim.SessionID)
	if err != nil || session == nil || session.State != claim.TargetState {
		s.logger.Error("provider terminal claim lost its state owner", zap.String("session_id", claim.SessionID), zap.Error(err))
		return
	}
	if changed {
		s.publishAcceptedTaskSessionState(ctx, claim.TaskID, claim.SessionID,
			claim.ExpectedState, claim.TargetState, claim.ErrorMessage, &updatedAt, session)
	}
	claimer, ok := s.repo.(terminalProviderAccessClaimer)
	if !ok {
		return
	}
	if err := claimer.ReleaseProviderAccessTerminal(ctx, claim.SessionID, claim.ClaimID); err != nil {
		s.logger.Error("failed to clear recovered provider terminal claim",
			zap.String("session_id", claim.SessionID), zap.Error(err))
	}
}
