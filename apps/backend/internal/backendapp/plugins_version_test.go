package backendapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/agent/registry"
	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/persistence/requiredstores"
	"github.com/kandev/kandev/internal/plugins"
	"github.com/kandev/kandev/internal/plugins/pkgtar/pkgtartest"
	"github.com/kandev/kandev/internal/plugins/store"
	"github.com/kandev/kandev/internal/secrets"
	"github.com/kandev/kandev/internal/startup"
	"github.com/kandev/kandev/internal/system/sessioncapacity"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

// TestProvideServicesWiresPluginsKandevVersion pins the production wiring of
// the running build version into the plugin service.
//
// internal/plugins has always been able to enforce a package's
// manifest.min_kandev_version, but checkMinKandevVersion short-circuits to
// nil while s.kandevVersion is empty — so for as long as SetKandevVersion had
// no production caller, the whole mechanism was a silent no-op in every
// shipped build while its unit tests stayed green. Only a test that goes
// through provideServices can catch that regressing again.
func TestProvideServicesWiresPluginsKandevVersion(t *testing.T) {
	const wantVersion = "9.8.7"

	services, _, _ := provideTestServices(t, wantVersion)
	if services.Plugins == nil {
		t.Fatal("provideServices returned a nil Plugins service")
	}
	if got := services.Plugins.KandevVersion(); got != wantVersion {
		t.Fatalf("Plugins.KandevVersion() = %q, want %q — min_kandev_version is unenforced without it", got, wantVersion)
	}
}

type exactHostRuntime struct{ host pluginsdk.Host }

type exactCommandFixture struct {
	record  *store.Record
	grant   tasksqlite.ExactTaskCommandGrant
	command tasksqlite.ExactTaskDescriptionCommand
	receipt tasksqlite.ExactTaskDescriptionReceipt
	task    *taskmodels.Task
}

func (r *exactHostRuntime) Start(_ context.Context, rec *store.Record, f func(string) pluginsdk.Host) error {
	r.host = f(rec.ID)
	return nil
}
func (*exactHostRuntime) Stop(string)                                {}
func (*exactHostRuntime) Get(string) (*pluginsdk.RemotePlugin, bool) { return nil, true }
func (*exactHostRuntime) Ping(string) error                          { return nil }
func (*exactHostRuntime) Running(string) bool                        { return false }
func (*exactHostRuntime) RestartCount(string) int                    { return 0 }
func (*exactHostRuntime) StopAll()                                   {}

func setupProductionExactCommand(t *testing.T, services *Services, repos *Repositories) exactCommandFixture {
	t.Helper()
	ctx := context.Background()
	if err := repos.Task.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "exact-ws", Name: "Exact"}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Task.CreateTask(ctx, &taskmodels.Task{ID: "exact-task", WorkspaceID: "exact-ws", Title: "Task"}); err != nil {
		t.Fatal(err)
	}
	rt := &exactHostRuntime{}
	services.Plugins.SetRuntime(rt)
	rec, err := services.Plugins.Install(ctx, exactHostPackage(t, "exact-host"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services.Plugins.GrantCapabilityApproval(rec.InstallationID, "exact-ws", 1, plugins.ManifestCapabilityDigest(rec.Manifest), []string{"host.v2.read:tasks", "host.v2.write:tasks"}, "human", "grant", "audit"); err != nil {
		t.Fatal(err)
	}
	h, ok := rt.host.(pluginsdk.ExactTaskHost)
	if !ok {
		t.Fatal("runtime did not receive exact Host")
	}
	items, page, err := h.ListTasksExact(ctx, pluginsdk.ExactTaskQuery{WorkspaceID: "exact-ws", CapabilityRevision: 1})
	if err != nil || len(items) != 1 || items[0].ID != "exact-task" || page.AuditID == "" {
		t.Fatalf("exact read: items=%+v page=%+v err=%v", items, page, err)
	}
	task, err := repos.Task.GetTask(ctx, "exact-task")
	if err != nil {
		t.Fatal(err)
	}
	fence, err := repos.Task.ExactTaskCommandWorkspaceFence(ctx, "exact-ws")
	if err != nil {
		t.Fatal(err)
	}
	d, err := services.Plugins.AuthorizeAndRecordExactCapability(rec.InstallationID, "exact-ws", "host.v2.write:tasks", 1, "exact-marker", "exact-command")
	if err != nil || !d.Allowed {
		t.Fatalf("exact write authorization = %+v, %v", d, err)
	}
	g := tasksqlite.ExactTaskCommandGrant{ID: "exact-grant", InstallationID: rec.InstallationID, WorkspaceID: "exact-ws", TaskID: task.ID, CapabilityID: "host.v2.write:tasks", ReceiptAuditID: d.Receipt.AuditID, ApprovalRevision: 1, ActionDigest: "marker", IdempotencyKey: "marker-key", ExpiresAt: time.Now().Add(time.Minute)}
	if err = repos.Task.IssueExactTaskCommandGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	command := exactDescriptionCommand(g, "[marker]", task.ResourceVersion, fence)
	receipt, err := repos.Task.ApplyExactTaskDescriptionCommand(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	return exactCommandFixture{record: rec, grant: g, command: command, receipt: receipt, task: task}
}

func exactDescriptionCommand(grant tasksqlite.ExactTaskCommandGrant, marker string, resourceVersion, fence int64) tasksqlite.ExactTaskDescriptionCommand {
	return tasksqlite.ExactTaskDescriptionCommand{
		GrantID:                 grant.ID,
		InstallationID:          grant.InstallationID,
		WorkspaceID:             grant.WorkspaceID,
		TaskID:                  grant.TaskID,
		CapabilityID:            grant.CapabilityID,
		ReceiptAuditID:          grant.ReceiptAuditID,
		ApprovalRevision:        grant.ApprovalRevision,
		ActionDigest:            grant.ActionDigest,
		IdempotencyKey:          grant.IdempotencyKey,
		Marker:                  marker,
		ExpectedResourceVersion: resourceVersion,
		ExpectedFence:           fence,
	}
}

func TestSetupProductionExactCommandPersistsMarker(t *testing.T) {
	services, _, repos := provideTestServices(t, "exact-command-fixture")
	fixture := setupProductionExactCommand(t, services, repos)
	stored, err := repos.Task.GetTask(context.Background(), fixture.task.ID)
	if err != nil || stored.Description != "[marker]" {
		t.Fatalf("fixture marker = %+v, %v", stored, err)
	}
}

func TestProvideServicesExactCommandProjectionSurvivesSQLiteRestart(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	servicesFirst, _, reposFirst, closeFirst := provideTestServicesWithCleanup(t, "exact-command-restart", home)
	fixture := setupProductionExactCommand(t, servicesFirst, reposFirst)

	fence, err := reposFirst.Task.ExactTaskCommandWorkspaceFence(ctx, fixture.task.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	revokedGrant := fixture.grant
	revokedGrant.ID = "restart-revoked-grant"
	revokedGrant.ActionDigest = "restart-revoked-marker"
	revokedGrant.IdempotencyKey = "restart-revoked-key"
	if err = reposFirst.Task.IssueExactTaskCommandGrant(ctx, revokedGrant); err != nil {
		t.Fatal(err)
	}
	expiredGrant := fixture.grant
	expiredGrant.ID = "restart-expired-grant"
	expiredGrant.ActionDigest = "restart-expired-marker"
	expiredGrant.IdempotencyKey = "restart-expired-key"
	if err = reposFirst.Task.IssueExactTaskCommandGrant(ctx, expiredGrant); err != nil {
		t.Fatal(err)
	}
	closeFirst()

	servicesSecond, _, reposSecond, closeSecond := provideTestServicesWithCleanup(t, "exact-command-restart", home)
	t.Cleanup(closeSecond)
	replay, err := reposSecond.Task.ApplyExactTaskDescriptionCommand(ctx, fixture.command)
	if err != nil || replay != fixture.receipt {
		t.Fatalf("restarted replay = %+v, %v; want %+v", replay, err, fixture.receipt)
	}
	stored, err := reposSecond.Task.GetTask(ctx, fixture.task.ID)
	if err != nil || stored.Description != "[marker]" || stored.ResourceVersion != fixture.receipt.ResourceVersion {
		t.Fatalf("restarted marker = %+v, %v", stored, err)
	}
	var auditCount int
	if err = reposSecond.Task.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM exact_task_command_audits WHERE audit_id = ?`, fixture.receipt.AuditID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("restarted audit count = %d, want 1", auditCount)
	}
	if _, err = reposSecond.Task.DB().ExecContext(ctx, `UPDATE exact_task_command_grants SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute), expiredGrant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = reposSecond.Task.ApplyExactTaskDescriptionCommand(ctx, exactDescriptionCommand(expiredGrant, "[expired]", stored.ResourceVersion, fence)); !errors.Is(err, tasksqlite.ErrExactTaskCommandUnavailable) {
		t.Fatalf("restarted expired command = %v", err)
	}
	if _, err = servicesSecond.Plugins.RevokeCapabilityApproval(fixture.record.InstallationID, fixture.task.WorkspaceID, 1, "human", "revoke", "restart-revoke"); err != nil {
		t.Fatal(err)
	}
	if _, err = reposSecond.Task.ApplyExactTaskDescriptionCommand(ctx, exactDescriptionCommand(revokedGrant, "[revoked]", stored.ResourceVersion, fence)); !errors.Is(err, tasksqlite.ErrExactTaskCommandUnavailable) {
		t.Fatalf("restarted revoked command = %v", err)
	}
	stored, err = reposSecond.Task.GetTask(ctx, fixture.task.ID)
	if err != nil || stored.Description != "[marker]" || stored.ResourceVersion != fixture.receipt.ResourceVersion {
		t.Fatalf("restarted denied effect = %+v, %v", stored, err)
	}
	if err = reposSecond.Task.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM exact_task_command_audits`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("restarted denied audit count = %d, want 1", auditCount)
	}
}

func TestProvideServicesInstalledHostRecordsExactReceipt(t *testing.T) {
	ctx := context.Background()
	services, _, repos := provideTestServices(t, "exact-host")
	fixture := setupProductionExactCommand(t, services, repos)
	storedMarker, err := repos.Task.GetTask(ctx, fixture.task.ID)
	if err != nil || storedMarker.Description != "[marker]" {
		t.Fatalf("exact marker = %+v, %v", storedMarker, err)
	}
	rec := fixture.record
	grant := fixture.grant
	if err = repos.Task.CreateTask(ctx, &taskmodels.Task{ID: "revoked-task", WorkspaceID: "exact-ws", Title: "Revoked", Description: "before"}); err != nil {
		t.Fatal(err)
	}
	revoked, err := repos.Task.GetTask(ctx, "revoked-task")
	if err != nil {
		t.Fatal(err)
	}
	fence, err := repos.Task.ExactTaskCommandWorkspaceFence(ctx, "exact-ws")
	if err != nil {
		t.Fatal(err)
	}
	grant.ID, grant.TaskID, grant.ActionDigest, grant.IdempotencyKey = "revoked-grant", revoked.ID, "revoked-marker", "revoked-key"
	if err = repos.Task.IssueExactTaskCommandGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err = services.Plugins.RevokeCapabilityApproval(rec.InstallationID, "exact-ws", 1, "human", "revoke", "revoke-audit"); err != nil {
		t.Fatal(err)
	}
	_, err = repos.Task.ApplyExactTaskDescriptionCommand(ctx, tasksqlite.ExactTaskDescriptionCommand{GrantID: grant.ID, InstallationID: grant.InstallationID, WorkspaceID: grant.WorkspaceID, TaskID: grant.TaskID, CapabilityID: grant.CapabilityID, ReceiptAuditID: grant.ReceiptAuditID, ApprovalRevision: 1, ActionDigest: grant.ActionDigest, IdempotencyKey: grant.IdempotencyKey, Marker: "[revoked]", ExpectedResourceVersion: revoked.ResourceVersion, ExpectedFence: fence})
	if !errors.Is(err, tasksqlite.ErrExactTaskCommandUnavailable) {
		t.Fatalf("revoked command = %v", err)
	}
	stored, err := repos.Task.GetTask(ctx, revoked.ID)
	if err != nil || stored.Description != "before" || stored.ResourceVersion != revoked.ResourceVersion {
		t.Fatalf("revoked task = %+v, %v", stored, err)
	}
}

func TestProvideServicesReadOnlyApprovalCannotMintExactCommandGrant(t *testing.T) {
	ctx := context.Background()
	services, _, repos := provideTestServices(t, "exact-read-only")
	if err := repos.Task.CreateWorkspace(ctx, &taskmodels.Workspace{ID: "read-only-ws", Name: "Read only"}); err != nil {
		t.Fatal(err)
	}
	if err := repos.Task.CreateTask(ctx, &taskmodels.Task{ID: "read-only-task", WorkspaceID: "read-only-ws", Title: "Task", Description: "before"}); err != nil {
		t.Fatal(err)
	}
	services.Plugins.SetRuntime(&exactHostRuntime{})
	record, err := services.Plugins.Install(ctx, exactHostPackage(t, "exact-read-only"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services.Plugins.GrantCapabilityApproval(record.InstallationID, "read-only-ws", 1, plugins.ManifestCapabilityDigest(record.Manifest), []string{"host.v2.read:tasks"}, "human", "grant", "audit"); err != nil {
		t.Fatal(err)
	}
	decision, err := services.Plugins.AuthorizeAndRecordExactCapability(record.InstallationID, "read-only-ws", "host.v2.write:tasks", 1, "exact-marker", "exact-command")
	if err == nil || decision.Allowed {
		t.Fatalf("read-only write authorization = %+v, %v", decision, err)
	}
	task, err := repos.Task.GetTask(ctx, "read-only-task")
	if err != nil {
		t.Fatal(err)
	}
	grant := tasksqlite.ExactTaskCommandGrant{ID: "read-only-grant", InstallationID: record.InstallationID, WorkspaceID: "read-only-ws", TaskID: task.ID, CapabilityID: "host.v2.read:tasks", ReceiptAuditID: "read-only-receipt", ApprovalRevision: 1, ActionDigest: "marker", IdempotencyKey: "read-only-key", ExpiresAt: time.Now().Add(time.Minute)}
	if err = repos.Task.IssueExactTaskCommandGrant(ctx, grant); !errors.Is(err, tasksqlite.ErrExactTaskCommandUnavailable) {
		t.Fatalf("IssueExactTaskCommandGrant with read capability = %v", err)
	}
	if _, err = repos.Task.ApplyExactTaskDescriptionCommand(ctx, tasksqlite.ExactTaskDescriptionCommand{GrantID: grant.ID, InstallationID: grant.InstallationID, WorkspaceID: grant.WorkspaceID, TaskID: grant.TaskID, CapabilityID: grant.CapabilityID, ReceiptAuditID: grant.ReceiptAuditID, ApprovalRevision: grant.ApprovalRevision, ActionDigest: grant.ActionDigest, IdempotencyKey: grant.IdempotencyKey, Marker: "[marker]", ExpectedResourceVersion: task.ResourceVersion, ExpectedFence: 0}); !errors.Is(err, tasksqlite.ErrExactTaskCommandUnavailable) {
		t.Fatalf("ApplyExactTaskDescriptionCommand with read capability = %v", err)
	}
	stored, err := repos.Task.GetTask(ctx, task.ID)
	if err != nil || stored.Description != "before" || stored.ResourceVersion != task.ResourceVersion {
		t.Fatalf("read-only command changed task: %+v, %v", stored, err)
	}
	var grants, audits int
	if err = repos.Task.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM exact_task_command_grants`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err = repos.Task.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM exact_task_command_audits`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if grants != 0 || audits != 0 {
		t.Fatalf("read-only command journal effects: grants=%d audits=%d", grants, audits)
	}
}

func exactHostPackage(t *testing.T, id string) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	p := goruntime.GOOS + "-" + goruntime.GOARCH
	manifest := fmt.Sprintf("id: %s\napi_version: 1\nversion: 1.0.0\nmin_kandev_version: 0.1.0\ndisplay_name: Exact\ncapabilities:\n  host_v2_read: [tasks]\n  host_v2_write: [tasks]\nruntime:\n  type: binary\n  executables:\n    %s: server/plugin\n", id, p)
	if err := pkgtartest.WritePackage(&b, map[string][]byte{"manifest.yaml": []byte(manifest), "server/plugin": []byte("#!/bin/sh\n")}); err != nil {
		t.Fatal(err)
	}
	return &b
}

func TestProvideServicesWiresExactCommandAuthorityToTaskSQLite(t *testing.T) {
	services, _, repos := provideTestServices(t, "exact-command-authority")
	if services.Plugins == nil || !services.Plugins.ExactTaskCommandAuthorityAvailable() {
		t.Fatal("provideServices did not bind the exact command authority")
	}
	if repos.Task == nil || !repos.Task.ExactTaskCommandAvailable() {
		t.Fatal("test bootstrap did not provide a SQLite exact command writer")
	}
}

func TestProvideOrchestratorInjectsQueueValidatorIntoExactTaskCommand(t *testing.T) {
	ctx := context.Background()
	services, cfg, repos := provideTestServices(t, "exact-command-orchestrator")
	agentRegistry, cleanup, err := registry.Provide(newTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	lifecycleMgr := lifecycle.NewManager(nil, nil, nil, nil, nil, nil, lifecycle.ExecutorFallbackDeny, t.TempDir(), newTestLogger())
	sqliteDB := sqlx.NewDb(repos.Task.DB(), "sqlite3")
	pool := db.NewPool(sqliteDB, sqliteDB)
	if _, _, err = provideOrchestrator(ctx, cfg, newTestLogger(), pool, bus.NewMemoryEventBus(newTestLogger()), repos.Task, nil, services.Plugins, services.Task, services.User, lifecycleMgr, agentRegistry, services.Workflow, nil, nil, nil, nil, nil, nil, sessioncapacity.ReadEnvironment()); err != nil {
		t.Fatalf("provideOrchestrator: %v", err)
	}

	queueRepository, err := messagequeue.NewSQLiteRepository(sqliteDB, sqliteDB)
	if err != nil {
		t.Fatal(err)
	}
	queue := queueRepository.(interface {
		messagequeue.Repository
		messagequeue.ExactPendingTransitionReader
	})
	for _, workspaceID := range []string{"exact-ws-a", "exact-ws-b"} {
		taskID, sessionID := workspaceID+"-task", workspaceID+"-session"
		if err = repos.Task.CreateWorkspace(ctx, &taskmodels.Workspace{ID: workspaceID, Name: workspaceID}); err != nil {
			t.Fatal(err)
		}
		if err = repos.Task.CreateTask(ctx, &taskmodels.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}); err != nil {
			t.Fatal(err)
		}
		if err = repos.Task.CreateTaskSession(ctx, &taskmodels.TaskSession{ID: sessionID, TaskID: taskID, State: taskmodels.TaskSessionStateCreated}); err != nil {
			t.Fatal(err)
		}
		if err = queue.SetPendingMove(ctx, sessionID, &messagequeue.PendingMove{TaskID: taskID, WorkflowID: "workflow", WorkflowStepID: "step"}); err != nil {
			t.Fatal(err)
		}
		snapshot, snapshotErr := queue.OpenExactPendingTransitionSnapshot(ctx, messagequeue.ExactPendingTransitionSnapshotRequest{WorkspaceID: workspaceID})
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		rows, pageErr := queue.PageExactPendingTransitionSnapshot(ctx, snapshot.Token, 0, 1)
		if pageErr != nil || len(rows) != 1 {
			t.Fatalf("pending rows for %s = %#v, %v", workspaceID, rows, pageErr)
		}
	}
	services.Plugins.SetRuntime(&exactHostRuntime{})
	record, err := services.Plugins.Install(ctx, exactHostPackage(t, "exact-command-orchestrator"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services.Plugins.GrantCapabilityApproval(record.InstallationID, "exact-ws-a", 1, plugins.ManifestCapabilityDigest(record.Manifest), []string{"host.v2.write:tasks"}, "human", "grant", "grant-audit"); err != nil {
		t.Fatal(err)
	}
	task, err := repos.Task.GetTask(ctx, "exact-ws-a-task")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := services.Plugins.AuthorizeAndRecordExactCapability(record.InstallationID, task.WorkspaceID, "host.v2.write:tasks", 1, "marker", "exact-command")
	if err != nil || !decision.Allowed {
		t.Fatalf("write decision = %+v, %v", decision, err)
	}
	grant := tasksqlite.ExactTaskCommandGrant{ID: "exact-command-orchestrator-grant", InstallationID: record.InstallationID, WorkspaceID: task.WorkspaceID, TaskID: task.ID, CapabilityID: "host.v2.write:tasks", ReceiptAuditID: decision.Receipt.AuditID, ApprovalRevision: 1, ActionDigest: "marker", IdempotencyKey: "command", ExpiresAt: time.Now().Add(time.Minute)}
	if err = repos.Task.IssueExactTaskCommandGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err = repos.Task.ApplyExactTaskDescriptionCommand(ctx, exactDescriptionCommand(grant, "[marker]", task.ResourceVersion, 0)); !errors.Is(err, tasksqlite.ErrExactTaskCommandUnavailable) {
		t.Fatalf("missing pending evidence command = %v", err)
	}
}

func TestRequiredStoreBootstrapCompleteness(t *testing.T) {
	_, _, repos := provideTestServices(t, "test-bootstrap-completeness")

	// Repository and service construction cover every catalog entry except the
	// three stores deliberately initialized in later startup phases.
	wantPending := []string{"message-queue", "delivery", "storage"}
	gotPending := repos.RequiredStores.UnavailableStoreIDs()
	if !reflect.DeepEqual(gotPending, wantPending) {
		t.Fatalf("pending required stores = %v, want %v", gotPending, wantPending)
	}
}

func TestRequiredStoreFailure(t *testing.T) {
	tracker, err := requiredstores.NewTracker([]requiredstores.Descriptor{{
		ID: "task", OwnerPackage: "internal/task", RequiredTables: []string{"tasks"},
		Sweep: startup.StepStoresRepositories,
	}})
	if err != nil {
		t.Fatalf("NewTracker: %v", err)
	}
	constructorErr := errors.New("schema constructor failed")
	got := recordRequiredStore(context.Background(), tracker, "task", constructorErr)
	if !errors.Is(got, constructorErr) {
		t.Fatalf("recordRequiredStore() error = %v, want %v", got, constructorErr)
	}
	if !strings.Contains(got.Error(), `required store "task"`) {
		t.Fatalf("recordRequiredStore() error = %q, want store ID", got)
	}
	if err := tracker.ValidateComplete(); err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("ValidateComplete() error = %v, want failed task store", err)
	}
}

func TestExternalProviderFailureIsolation(t *testing.T) {
	t.Setenv("KANDEV_MOCK_GITHUB", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", t.TempDir())

	pool := newMessageQueueSettingsTestPool(t)
	log := newTestLogger()
	externalErr := errors.New("credential backend unavailable")
	service, cleanup, err := initGitHubServiceRequired(
		&config.Config{},
		pool,
		bus.NewMemoryEventBus(log),
		failingExternalSecretStore{err: externalErr},
		log,
	)
	if err != nil {
		t.Fatalf("external credential failure made local store fatal: %v", err)
	}
	if service == nil {
		t.Fatal("GitHub service is nil after external credential failure")
	}
	if cleanup != nil {
		t.Cleanup(func() { _ = cleanup() })
	}
	if _, err := service.GetWorkspaceSettings(context.Background(), "external-failure-workspace"); err != nil {
		t.Fatalf("local GitHub store unavailable after credential failure: %v", err)
	}
}

// provideTestServices boots the real service graph over a temp-dir SQLite
// database, the same way Run does, and returns the resulting services.
func provideTestServices(t *testing.T, version string) (*Services, *config.Config, *Repositories) {
	t.Helper()
	services, cfg, repos, close := provideTestServicesWithCleanup(t, version, t.TempDir())
	t.Cleanup(close)
	return services, cfg, repos
}

func provideTestServicesWithCleanup(t *testing.T, version, home string) (*Services, *config.Config, *Repositories, func()) {
	t.Helper()

	cfg := &config.Config{
		HomeDir:  home,
		Database: config.DatabaseConfig{Driver: "sqlite"},
	}
	log := newTestLogger()

	pool, repos, cleanups, err := provideRepositories(context.Background(), cfg, log, version)
	if err != nil {
		t.Fatalf("provideRepositories: %v", err)
	}
	var services *Services
	closed := false
	close := func() {
		if closed {
			return
		}
		closed = true
		if services.PluginsCleanup != nil {
			_ = services.PluginsCleanup()
		}
		if services.Workflow != nil {
			_ = services.Workflow.Close()
		}
		for i := len(cleanups) - 1; i >= 0; i-- {
			if cleanups[i] != nil {
				_ = cleanups[i]()
			}
		}
	}

	agentRegistry, registryCleanup, err := registry.Provide(log)
	if err != nil {
		t.Fatalf("registry.Provide: %v", err)
	}

	services, _, err = provideServices(context.Background(), cfg, log, repos, pool, bus.NewMemoryEventBus(log), agentRegistry, version)
	if err != nil {
		t.Fatalf("provideServices: %v", err)
	}
	if registryCleanup != nil {
		previous := close
		close = func() { previous(); _ = registryCleanup() }
	}
	return services, cfg, repos, close
}

func TestProvideTestServicesCleanupReopensSQLiteHome(t *testing.T) {
	home := t.TempDir()
	_, firstCfg, _, closeFirst := provideTestServicesWithCleanup(t, "fixture-reopen", home)
	closeFirst()
	_, secondCfg, _, closeSecond := provideTestServicesWithCleanup(t, "fixture-reopen", home)
	t.Cleanup(closeSecond)
	if firstCfg.HomeDir != secondCfg.HomeDir {
		t.Fatalf("reopened home = %q, want %q", secondCfg.HomeDir, firstCfg.HomeDir)
	}
}

type failingExternalSecretStore struct {
	emptySecretStore
	err error
}

func (s failingExternalSecretStore) List(context.Context) ([]*secrets.SecretListItem, error) {
	return nil, s.err
}
