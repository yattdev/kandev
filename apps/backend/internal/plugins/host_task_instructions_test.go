package plugins

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/plugins/manifest"
	"github.com/kandev/kandev/internal/plugins/state"
	pluginstore "github.com/kandev/kandev/internal/plugins/store"
	"github.com/kandev/kandev/internal/task/taskinstructions"
	"github.com/kandev/kandev/pkg/pluginsdk"
	_ "github.com/mattn/go-sqlite3"
)

type instructionBindingWriterFake struct {
	binding *taskinstructions.DurableBinding
	writes  int
}

func (f *instructionBindingWriterFake) GetTaskInstructionBinding(_ context.Context, _ string) (*taskinstructions.DurableBinding, error) {
	if f.binding == nil {
		return nil, taskinstructions.ErrBindingNotFound
	}
	return f.binding, nil
}

func (f *instructionBindingWriterFake) PutTaskInstructionBindingExact(_ context.Context, request taskinstructions.WriteRequest) (*taskinstructions.DurableBinding, error) {
	f.writes++
	f.binding = &taskinstructions.DurableBinding{TaskID: request.TaskID, WorkspaceID: request.WorkspaceID, EnvironmentID: request.EnvironmentID,
		OwnershipGeneration: request.ExpectedOwnershipGeneration, Version: request.ExpectedVersion + 1,
		OwnerInstallationID: request.ManagementInstallationID, OwnerInstanceKey: request.ManagementInstanceKey,
		Content: request.Content, Digest: request.Digest}
	return f.binding, nil
}

func TestExactTaskInstructionsRequireCoordinatorCapabilityAndReplayReceipt(t *testing.T) {
	connection, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	commands, err := state.NewCommandStore(db.NewPool(connection, connection))
	if err != nil {
		t.Fatal(err)
	}
	record := &pluginstore.Record{Manifest: manifest.Manifest{ID: "coordinator-plugin", Capabilities: manifest.Capabilities{APIRead: []string{"task_instructions"}, APIWrite: []string{"task_instructions"}}}, InstallationID: "coordinator-installation"}
	registry := NewRegistry()
	registry.Add(record)
	svc := NewService(pluginstore.NewFSStore(t.TempDir()), registry, nil, testLogger(t))
	if err := svc.SetPluginsDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.SetExactCommandStore(commands)
	digest := ManifestCapabilityDigest(record.Manifest)
	if _, err := svc.approvalGrant(record.InstallationID, "instructions-workspace", 1, digest,
		[]string{taskInstructionsReadCapability, taskInstructionsWriteCapability}, "human", "grant", "instructions-approval"); err != nil {
		t.Fatal(err)
	}
	writer := &instructionBindingWriterFake{}
	svc.SetTaskInstructionBindingWriter(writer)
	host := svc.hostForPlugin(record.ID).(*pluginHost)
	manager, ok := pluginsdk.HostTaskInstructions(host)
	if !ok {
		t.Fatal("task instruction Host extension unavailable")
	}
	input := pluginsdk.ExactTaskInstructionsPut{RequestID: "instruction-request", WorkspaceID: "instructions-workspace", TaskID: "instruction-task", EnvironmentID: "instruction-env",
		ExpectedTaskResourceVersion: "2026-10-11T12:00:00Z", ExpectedOwnershipGeneration: 1, ExpectedVersion: 0,
		ManagementInstanceKey: "coordinator-main", ExpectedClaimGeneration: 1, Content: "task instructions", IdempotencyKey: "instruction-write",
		ApprovalRevision: 1, ManifestDigest: digest}
	result, written, err := manager.Put(context.Background(), input)
	if err != nil || result.Status != pluginsdk.CommandApplied || written.Version != 1 || writer.writes != 1 {
		t.Fatalf("first Put = result:%+v binding:%+v writes:%d err:%v", result, written, writer.writes, err)
	}
	replayed, got, err := manager.Put(context.Background(), input)
	if err != nil || replayed.Status != pluginsdk.CommandApplied || got.Digest != written.Digest || writer.writes != 1 {
		t.Fatalf("replayed Put = result:%+v binding:%+v writes:%d err:%v", replayed, got, writer.writes, err)
	}
	read, err := manager.Get(context.Background(), pluginsdk.ExactTaskInstructionsGet{WorkspaceID: input.WorkspaceID, TaskID: input.TaskID, ApprovalRevision: 1, ManifestDigest: digest})
	if err != nil || read.OwnerInstallationID != record.InstallationID || read.Content != input.Content {
		t.Fatalf("Get = binding:%+v err:%v", read, err)
	}
	foreign, err := manager.Get(context.Background(), pluginsdk.ExactTaskInstructionsGet{WorkspaceID: "foreign-workspace", TaskID: input.TaskID, ApprovalRevision: 1, ManifestDigest: digest})
	if err == nil || foreign.TaskID != "" {
		t.Fatalf("foreign Get = binding:%+v err:%v, want hidden binding", foreign, err)
	}
}
