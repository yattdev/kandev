package github

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
)

// beginPRMutation serializes association insert/restore across processes, including
// ordinary writers. It imposes no uniqueness rule on existing manual links.
func (s *Store) beginPRMutation(ctx context.Context) (*sqlx.Tx, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if dialect.IsPostgres(s.db.DriverName()) {
		_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('github-task-pr-associations',0))`)
	} else {
		// Acquire SQLite's writer reservation before any admission snapshot.
		_, err = tx.ExecContext(ctx, `UPDATE github_task_prs SET updated_at=updated_at WHERE 1=0`)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Store) checkScopedPRURLAdmission(ctx context.Context, req ScopedPRURLRequest) error {
	tx, err := s.beginPRMutation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	return s.scopedPRURLAdmissionTx(ctx, tx, req)
}

func (s *Store) scopedPRURLAdmissionTx(ctx context.Context, tx *sqlx.Tx, req ScopedPRURLRequest) error {
	p, ok := scope.PrincipalFromContext(ctx)
	if !ok || p.WorkspaceID != req.WorkspaceID || (p.Surface != profile.SurfaceKanbanTask && p.Surface != profile.SurfaceOfficeTask) {
		return errors.New("trusted active parent session required")
	}
	query := `SELECT caller.metadata AS caller_metadata, session.metadata AS session_metadata
 FROM tasks caller JOIN tasks target ON target.parent_id=caller.id
 JOIN task_sessions session ON session.task_id=caller.id
 JOIN task_repositories tr ON tr.task_id=target.id
 JOIN repositories repo ON repo.id=tr.repository_id
 WHERE caller.id=? AND target.id=? AND session.id=? AND session.state='RUNNING'
 AND caller.workspace_id=? AND target.workspace_id=? AND repo.workspace_id=?
 AND caller.origin NOT IN ('coordinator','automation_run') AND caller.archived_at IS NULL AND target.archived_at IS NULL AND tr.repository_id=?
 AND repo.provider='github' AND LOWER(repo.provider_owner)=? AND LOWER(repo.provider_name)=?
 AND repo.provider_host IN ('github.com','https://github.com','https://github.com/')`
	if dialect.IsPostgres(s.db.DriverName()) {
		query += ` FOR SHARE OF caller,target,session,tr,repo`
	}
	var row struct {
		CallerMetadata  string `db:"caller_metadata"`
		SessionMetadata string `db:"session_metadata"`
	}
	if err := tx.GetContext(ctx, &row, tx.Rebind(query), p.CallerTaskID, req.TaskID, p.CallerSessionID, req.WorkspaceID, req.WorkspaceID, req.WorkspaceID, req.RepositoryID, strings.ToLower(req.SourceOwner), strings.ToLower(req.SourceRepo)); err != nil {
		return errors.New("active direct-parent repository authority could not be verified")
	}
	var callerMeta, sessionMeta map[string]interface{}
	if json.Unmarshal([]byte(row.CallerMetadata), &callerMeta) != nil || json.Unmarshal([]byte(row.SessionMetadata), &sessionMeta) != nil {
		return errors.New("session policy could not be verified")
	}
	if config, _ := sessionMeta["config_mode"].(bool); config {
		return errors.New("configuration session cannot associate PRs")
	}
	_, managed, err := models.ManagedToolPolicyFromTask(&models.Task{WorkspaceID: req.WorkspaceID, Metadata: callerMeta})
	if managed || err != nil {
		return errors.New("managed conversation cannot associate PRs")
	}
	return nil
}

func (s *Store) associateScopedPRURL(ctx context.Context, req ScopedPRURLRequest, pr *PR) (*TaskPR, bool, error) {
	tx, err := s.beginPRMutation(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = s.scopedPRURLAdmissionTx(ctx, tx, req); err != nil {
		return nil, false, err
	}
	row, changed, err := s.scopedPRURLMutationTx(ctx, tx, req, pr)
	if err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return row, changed, nil
}

func (s *Store) scopedPRURLMutationTx(ctx context.Context, tx *sqlx.Tx, req ScopedPRURLRequest, pr *PR) (*TaskPR, bool, error) {
	var existing TaskPR
	query := `SELECT ` + taskPRColumns + ` FROM github_task_prs WHERE task_id=? AND repository_id=? AND pr_number=?`
	if dialect.IsPostgres(s.db.DriverName()) {
		query += ` FOR UPDATE`
	}
	err := tx.GetContext(ctx, &existing, tx.Rebind(query), req.TaskID, req.RepositoryID, pr.Number)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	found := err == nil
	if found && (!strings.EqualFold(strings.TrimSuffix(existing.PRURL, "/"), strings.TrimSuffix(pr.HTMLURL, "/")) || existing.DetachedAt == nil && !strings.EqualFold(existing.HeadSHA, pr.HeadSHA)) {
		return nil, false, errors.New("canonical PR slot or stored head conflicts")
	}
	var competing int
	err = tx.GetContext(ctx, &competing, tx.Rebind(`SELECT COUNT(*) FROM github_task_prs p JOIN tasks t ON t.id=p.task_id WHERE t.workspace_id=? AND t.archived_at IS NULL AND p.detached_at IS NULL AND p.task_id<>? AND LOWER(p.owner)=? AND LOWER(p.repo)=? AND p.pr_number=?`), req.WorkspaceID, req.TaskID, strings.ToLower(pr.RepoOwner), strings.ToLower(pr.RepoName), pr.Number)
	if err != nil {
		return nil, false, err
	}
	if competing > 0 {
		return nil, false, errors.New("canonical PR is linked to another active task")
	}
	if found && existing.DetachedAt == nil {
		return &existing, false, hydrateTaskPRWorkflowAttention(&existing)
	}
	status := &PRStatus{PR: pr, OutcomeFieldsPopulated: true}
	if found {
		row, err := restoreTaskPRTx(ctx, tx, req.TaskID, req.RepositoryID, status)
		return row, true, err
	}
	row := &TaskPR{WorkspaceID: req.WorkspaceID, TaskID: req.TaskID, RepositoryID: req.RepositoryID, Owner: pr.RepoOwner, Repo: pr.RepoName, PRNumber: pr.Number, PRURL: pr.HTMLURL, PRTitle: pr.Title, HeadBranch: pr.HeadBranch, BaseBranch: pr.BaseBranch, HeadSHA: pr.HeadSHA, AuthorLogin: pr.AuthorLogin, State: pr.State, Additions: pr.Additions, Deletions: pr.Deletions, CreatedAt: pr.CreatedAt, MergedAt: pr.MergedAt, ClosedAt: pr.ClosedAt, Source: TaskPRSourceURLLink}
	saved, err := replaceTaskPRTx(ctx, tx, row, status)
	return saved, true, err
}
