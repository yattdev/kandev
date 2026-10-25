import { describe, expect, it } from "vitest";
import { mergeTaskUpdate } from "./task-merge";
import type { KanbanTask, TaskEventPayload } from "./task-archive-cache";

const oldTask = (): KanbanTask =>
  ({
    id: "dependent",
    workflowId: "workflow",
    workflowStepId: "step",
    title: "Current title",
    position: 0,
    updatedAt: "2026-10-25T12:00:00Z",
    dependencyRevision: 2,
    blocked: true,
    blockedReason: "pending",
    dependsOn: [{ id: "predecessor" }],
    blocks: [],
  }) as KanbanTask;

describe("dependency revision task-event merge", () => {
  it("keeps newer dependency data when an older event arrives", () => {
    const existing = oldTask();
    const payload: TaskEventPayload = {
      task_id: existing.id,
      workflow_id: "workflow",
      dependency_revision: 1,
      blocked: false,
      depends_on: [],
      blocks: [],
    };
    const next = {
      ...existing,
      title: "Other metadata",
      dependencyRevision: 1,
      blocked: false,
      dependsOn: [],
    };
    const merged = mergeTaskUpdate(existing, next, payload);
    expect(merged.title).toBe("Other metadata");
    expect(merged.dependencyRevision).toBe(2);
    expect(merged.dependsOn).toEqual([{ id: "predecessor" }]);
  });

  it("applies a newer dependency snapshot despite an older task timestamp", () => {
    const existing = oldTask();
    const payload: TaskEventPayload = {
      task_id: existing.id,
      workflow_id: "workflow",
      dependency_revision: 3,
      blocked: false,
      depends_on: [],
      blocks: [],
    };
    const next = {
      ...existing,
      title: "Old title",
      updatedAt: "2026-10-25T11:00:00Z",
      dependencyRevision: 3,
      blocked: false,
      blockedReason: undefined,
      dependsOn: [],
    };
    const merged = mergeTaskUpdate(existing, next, payload);
    expect(merged.title).toBe("Current title");
    expect(merged.dependencyRevision).toBe(3);
    expect(merged.dependsOn).toEqual([]);
  });
});
