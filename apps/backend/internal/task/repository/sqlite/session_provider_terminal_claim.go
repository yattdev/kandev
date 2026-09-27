package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
)

const (
	terminalProviderClaimKey    = "provider_access_terminal_claim"
	terminalProviderRecoveryKey = "provider_access_terminal_recovery"
)

var ErrTerminalProviderClaimPending = errors.New("provider access terminal claim pending")

// ClaimProviderAccessTerminal commits an ownership reservation before any
// provider call. Executor rotation takes the same session-row lock and refuses
// a pending claim, so the validated execution cannot be replaced during revoke.
func (r *Repository) ClaimProviderAccessTerminal(
	ctx context.Context, claim models.TerminalProviderAccessClaim,
) (string, bool, error) {
	if !validTerminalProviderClaim(claim) {
		return "", false, nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockTaskSessionRow(ctx, tx, claim.SessionID)
	if err != nil || !locked {
		return "", false, err
	}
	var taskID, state, metadata string
	err = tx.QueryRowContext(ctx, tx.Rebind(`SELECT task_id, state, COALESCE(metadata, '{}') FROM task_sessions WHERE id = ?`), claim.SessionID).
		Scan(&taskID, &state, &metadata)
	if err != nil {
		return "", false, err
	}
	if taskID != claim.TaskID || state != string(claim.ExpectedState) || hasTerminalProviderClaim(metadata) {
		return "", false, nil
	}
	ownerMatches, err := r.terminalClaimOwnerMatches(ctx, tx, claim, metadata)
	if err != nil || !ownerMatches {
		return "", false, err
	}
	claimID := uuid.NewString()
	if err := r.writeTerminalProviderClaimTx(ctx, tx, claim, claimID); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return claimID, true, nil
}

func validTerminalProviderClaim(claim models.TerminalProviderAccessClaim) bool {
	return claim.TaskID != "" && claim.SessionID != "" && claim.ExpectedState != "" &&
		(!claim.RequireExecution || claim.AgentExecutionID != "") &&
		(!claim.RequireNoExecution || claim.AgentExecutionID == "") &&
		(!claim.RequireExecution || !claim.RequireNoExecution)
}

func (r *Repository) writeTerminalProviderClaimTx(ctx context.Context, tx *sqlx.Tx, claim models.TerminalProviderAccessClaim, claimID string) error {
	recovery, err := json.Marshal(models.TerminalProviderAccessRecovery{
		TaskID: claim.TaskID, SessionID: claim.SessionID, ClaimID: claimID,
		ExpectedState: claim.ExpectedState, TargetState: claim.TargetState, ErrorMessage: claim.ErrorMessage,
	})
	if err != nil {
		return err
	}
	var query string
	if dialect.IsPostgres(r.db.DriverName()) {
		query = `UPDATE task_sessions SET metadata = jsonb_set(jsonb_set(` + postgresMetadataObject + `, '{` + terminalProviderClaimKey + `}', to_jsonb(?::text), true), '{` + terminalProviderRecoveryKey + `}', ?::jsonb, true)::text WHERE id = ?`
	} else {
		query = `UPDATE task_sessions SET metadata = json_set(json_set(` + sqliteMetadataObject + `, '$.` + terminalProviderClaimKey + `', ?), '$.` + terminalProviderRecoveryKey + `', json(?)) WHERE id = ?`
	}
	_, err = tx.ExecContext(ctx, tx.Rebind(query), claimID, string(recovery), claim.SessionID)
	return err
}

func (r *Repository) terminalClaimOwnerMatches(
	ctx context.Context, tx *sqlx.Tx, claim models.TerminalProviderAccessClaim, metadata string,
) (bool, error) {
	if claim.RequireNoExecution {
		var count int
		err := tx.QueryRowContext(ctx, tx.Rebind(`SELECT COUNT(*) FROM executors_running WHERE session_id = ?`), claim.SessionID).Scan(&count)
		if err != nil || count != 0 {
			return false, err
		}
	}
	if claim.AgentExecutionID != "" {
		matches, err := r.terminalClaimExecutionMatches(ctx, tx, claim)
		if err != nil || !matches {
			return matches, err
		}
	}
	if claim.CheckErrorStamp {
		stamp, err := metadataRecordStamp(metadata, models.SessionMetaKeyLastAgentError)
		if err != nil || stamp != claim.ExpectedErrorStamp {
			return false, err
		}
	}
	if claim.RequireNoStartAttempt {
		matches, err := noStartAttemptMatches(metadata)
		if err != nil || !matches {
			return matches, err
		}
	}
	return startAttemptMatches(metadata, claim.ExpectedStartAttemptID)
}

func startAttemptMatches(metadata, expected string) (bool, error) {
	if expected == "" {
		return true, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadata), &values); err != nil {
		return false, err
	}
	var attempt string
	if err := json.Unmarshal(values[models.SessionMetaKeyAgentStartAttemptID], &attempt); err != nil {
		return false, nil
	}
	return attempt == expected, nil
}

func noStartAttemptMatches(metadata string) (bool, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadata), &values); err != nil {
		return false, err
	}
	value := values[models.SessionMetaKeyAgentStartAttemptID]
	return len(value) == 0 || string(value) == "null" || string(value) == `""`, nil
}

func (r *Repository) terminalClaimExecutionMatches(
	ctx context.Context, tx *sqlx.Tx, claim models.TerminalProviderAccessClaim,
) (bool, error) {
	var executionID string
	err := tx.QueryRowContext(ctx, tx.Rebind(`SELECT agent_execution_id FROM executors_running WHERE session_id = ?`), claim.SessionID).Scan(&executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return !claim.RequireExecution, nil
	}
	if err != nil {
		return false, err
	}
	return executionID == claim.AgentExecutionID, nil
}

// ReleaseProviderAccessTerminal clears only the matching reservation. A failed
// cleanup leaves rotation fenced until the same claim can be reconciled.
func (r *Repository) ReleaseProviderAccessTerminal(ctx context.Context, sessionID, claimID string) error {
	if sessionID == "" || claimID == "" {
		return ErrTerminalProviderClaimPending
	}
	var query string
	if dialect.IsPostgres(r.db.DriverName()) {
		query = `UPDATE task_sessions SET metadata = (` + postgresMetadataObject + ` - '` + terminalProviderClaimKey + `' - '` + terminalProviderRecoveryKey + `')::text WHERE id = ? AND jsonb_extract_path_text(` + postgresMetadataObject + `, '` + terminalProviderClaimKey + `') = ?`
	} else {
		query = `UPDATE task_sessions SET metadata = json_remove(` + sqliteMetadataObject + `, '$.` + terminalProviderClaimKey + `', '$.` + terminalProviderRecoveryKey + `') WHERE id = ? AND json_extract(` + sqliteMetadataObject + `, '$.` + terminalProviderClaimKey + `') = ?`
	}
	result, err := r.db.ExecContext(ctx, r.db.Rebind(query), sessionID, claimID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("provider access terminal claim %s no longer owns session %s", claimID, sessionID)
	}
	return nil
}

// ListPendingProviderAccessTerminalClaims exposes non-secret interrupted
// reservations to the restart reconciler; ordinary session reads hide them.
func (r *Repository) ListPendingProviderAccessTerminalClaims(ctx context.Context) ([]models.TerminalProviderAccessRecovery, error) {
	rows, err := r.db.QueryContext(ctx, r.db.Rebind(`SELECT id, task_id, COALESCE(metadata, '{}') FROM task_sessions WHERE `+
		"NOT ("+terminalProviderClaimAbsentPredicate(r.db.DriverName())+") ORDER BY id"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var pending []models.TerminalProviderAccessRecovery
	for rows.Next() {
		var sessionID, taskID, metadata string
		if err := rows.Scan(&sessionID, &taskID, &metadata); err != nil {
			return nil, err
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal([]byte(metadata), &values); err != nil {
			return nil, err
		}
		var claimID string
		if err := json.Unmarshal(values[terminalProviderClaimKey], &claimID); err != nil {
			return nil, err
		}
		var recovery models.TerminalProviderAccessRecovery
		if len(values[terminalProviderRecoveryKey]) != 0 {
			if err := json.Unmarshal(values[terminalProviderRecoveryKey], &recovery); err != nil {
				return nil, err
			}
		}
		if recovery.ClaimID != "" && recovery.ClaimID != claimID {
			return nil, ErrTerminalProviderClaimPending
		}
		recovery.ClaimID, recovery.SessionID, recovery.TaskID = claimID, sessionID, taskID
		pending = append(pending, recovery)
	}
	return pending, rows.Err()
}

func hasTerminalProviderClaim(metadata string) bool {
	if strings.TrimSpace(metadata) == "" || strings.TrimSpace(metadata) == "null" {
		return false
	}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(metadata), &values) != nil {
		return true
	}
	value := values[terminalProviderClaimKey]
	return len(value) != 0 && string(value) != "null" && string(value) != `""`
}

func terminalProviderClaimAbsentPredicate(driver string) string {
	if dialect.IsPostgres(driver) {
		return "jsonb_extract_path_text(" + postgresMetadataObject + ", '" + terminalProviderClaimKey + "') IS NULL"
	}
	return "json_extract(" + sqliteMetadataObject + ", '$." + terminalProviderClaimKey + "') IS NULL"
}

func terminalProviderClaimMatchPredicate(driver string) string {
	if dialect.IsPostgres(driver) {
		return "jsonb_extract_path_text(" + postgresMetadataObject + ", '" + terminalProviderClaimKey + "') = ?"
	}
	return "json_extract(" + sqliteMetadataObject + ", '$." + terminalProviderClaimKey + "') = ?"
}

// UpdateTaskSessionStateIfCurrentClaim is the terminal owner's state CAS.
func (r *Repository) UpdateTaskSessionStateIfCurrentClaim(
	ctx context.Context, id string, expected, status models.TaskSessionState, errorMessage, claimID string,
) (bool, time.Time, error) {
	now := r.nowUTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_sessions SET state = ?, error_message = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND `+terminalProviderClaimMatchPredicate(r.db.DriverName())),
		string(status), errorMessage, completedAtForTaskSessionState(status, now), now, id, string(expected), claimID)
	if err != nil {
		return false, time.Time{}, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, now, err
}

// CancelActiveTaskSessionWithClaim preserves the strict transition's active
// state predicate while requiring its exact reservation.
func (r *Repository) CancelActiveTaskSessionWithClaim(
	ctx context.Context, id, reason string, expectedState models.TaskSessionState, claimID string,
) (bool, time.Time, error) {
	now := r.nowUTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		UPDATE task_sessions SET state = ?, error_message = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND state = ?
		AND `+terminalProviderClaimMatchPredicate(r.db.DriverName())),
		string(models.TaskSessionStateCancelled), reason, now, now, id, string(expectedState), claimID)
	if err != nil {
		return false, time.Time{}, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, now, err
}
