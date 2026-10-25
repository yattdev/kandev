import { describe, expect, it } from "vitest";
import { preserveOmittedDependencyFields } from "./task-dependencies";
import type { KanbanState } from "@/lib/state/slices/kanban/types";

type KanbanTask = KanbanState["tasks"][number];

const existing = (): KanbanTask =>
  ({
    id: "b",
    workflowId: "wf-1",
    workflowStepId: "s",
    title: "B",
    position: 0,
    blocked: true,
    blockedReason: "pending",
    dependsOn: [{ id: "a", title: "A", status: "pending" }],
    blocks: [{ id: "c", title: "C" }],
    startWhenUnblocked: true,
  }) as KanbanTask;

/** What toKanbanTask produces for an event with no dependency fields. */
const mergedFromBareEvent = (): KanbanTask =>
  ({
    id: "b",
    workflowId: "wf-1",
    workflowStepId: "s",
    title: "B",
    position: 0,
    blocked: false,
    dependsOn: [],
    blocks: [],
    startWhenUnblocked: false,
  }) as KanbanTask;

describe("preserveOmittedDependencyFields", () => {
  it("restores edges when the event does not mention dependencies", () => {
    // The regression this exists for: a task.updated fired by ordinary agent
    // activity carries no dependency fields, so without this the hydrated edges
    // were replaced by empty arrays and the dependency chip disappeared.
    const merged = mergedFromBareEvent();
    preserveOmittedDependencyFields(existing(), merged, { task_id: "b", title: "B" });
    expect(merged.blocked).toBe(true);
    expect(merged.blockedReason).toBe("pending");
    expect(merged.dependsOn).toHaveLength(1);
    expect(merged.blocks).toHaveLength(1);
    expect(merged.startWhenUnblocked).toBe(true);
  });

  it("lets an explicit empty list clear the edges", () => {
    // Removing the last edge publishes a payload that DOES mention dependencies
    // with an empty list; that is a real clear and must not be reverted.
    const merged = mergedFromBareEvent();
    preserveOmittedDependencyFields(existing(), merged, { depends_on: [], blocks: [] });
    expect(merged.blocked).toBe(false);
    expect(merged.dependsOn).toEqual([]);
    expect(merged.blocks).toEqual([]);
  });

  it("lets an explicit blocked flag through even without the lists", () => {
    const merged = mergedFromBareEvent();
    preserveOmittedDependencyFields(existing(), merged, { blocked: false });
    expect(merged.blocked).toBe(false);
  });

  it("ignores older dependency revisions while keeping unrelated updates", () => {
    const current = { ...existing(), dependencyRevision: 4 };
    const incoming = { ...mergedFromBareEvent(), dependencyRevision: 3 };
    preserveOmittedDependencyFields(current, incoming, {
      blocked: false,
      depends_on: [],
      blocks: [],
      dependency_revision: 3,
    });
    expect(incoming.dependencyRevision).toBe(4);
    expect(incoming.dependsOn).toEqual(current.dependsOn);
    expect(incoming.blocked).toBe(true);
  });

  it("accepts a newer dependency revision", () => {
    const current = { ...existing(), dependencyRevision: 4 };
    const incoming = { ...mergedFromBareEvent(), dependencyRevision: 5 };
    preserveOmittedDependencyFields(current, incoming, {
      blocked: false,
      depends_on: [],
      blocks: [],
      dependency_revision: 5,
    });
    expect(incoming.dependencyRevision).toBe(5);
    expect(incoming.dependsOn).toEqual([]);
    expect(incoming.startWhenUnblocked).toBe(true);
  });
});
