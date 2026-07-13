import { describe, it, expect, vi, beforeEach } from "vitest";
import type { StoreApi } from "zustand";
import type { AppState } from "@/lib/state/store";

import { registerTaskNotesHandlers } from "./task-notes";

const TASK_ID = "task-1";
const OTHER_TASK_ID = "task-2";
const ACTION_UPDATED = "task.notes.updated";
const ACTION_DELETED = "task.notes.deleted";

function makePayload(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    task_id: TASK_ID,
    content: "# Notes",
    created_at: "2026-04-20T00:00:00Z",
    updated_at: "2026-04-20T00:00:00Z",
    ...overrides,
  };
}

function makeMessage(action: string, payload: Record<string, unknown>) {
  return { id: "msg-1", type: "notification", action, payload };
}

function makeStore(overrides: Record<string, unknown> = {}) {
  const state = {
    tasks: { activeTaskId: TASK_ID, activeSessionId: "s-1" },
    taskNotes: { byTaskId: {} },
    setTaskNotes: vi.fn(),
    ...overrides,
  };
  return {
    getState: () => state as unknown as AppState,
    setState: vi.fn(),
    subscribe: vi.fn(),
    destroy: vi.fn(),
    getInitialState: vi.fn(),
  } as unknown as StoreApi<AppState>;
}

describe("task.notes.* handlers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("task.notes.updated: stores notes using task_id from payload", () => {
    const store = makeStore();
    const handlers = registerTaskNotesHandlers(store);

    handlers[ACTION_UPDATED]!(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeMessage(ACTION_UPDATED, makePayload()) as any,
    );

    expect(store.getState().setTaskNotes).toHaveBeenCalledWith(TASK_ID, expect.objectContaining({
      task_id: TASK_ID,
      content: "# Notes",
    }));
  });

  it("task.notes.updated: stores notes for any task (not just active)", () => {
    const store = makeStore();
    const handlers = registerTaskNotesHandlers(store);

    handlers[ACTION_UPDATED]!(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeMessage(ACTION_UPDATED, makePayload({ task_id: OTHER_TASK_ID })) as any,
    );

    expect(store.getState().setTaskNotes).toHaveBeenCalledWith(OTHER_TASK_ID, expect.objectContaining({
      task_id: OTHER_TASK_ID,
    }));
  });

  it("task.notes.deleted: clears notes using task_id from payload", () => {
    const store = makeStore();
    const handlers = registerTaskNotesHandlers(store);

    handlers[ACTION_DELETED]!(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeMessage(ACTION_DELETED, { task_id: TASK_ID }) as any,
    );

    expect(store.getState().setTaskNotes).toHaveBeenCalledWith(TASK_ID, null);
  });

  it("task.notes.deleted: clears notes for any task", () => {
    const store = makeStore();
    const handlers = registerTaskNotesHandlers(store);

    handlers[ACTION_DELETED]!(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      makeMessage(ACTION_DELETED, { task_id: OTHER_TASK_ID }) as any,
    );

    expect(store.getState().setTaskNotes).toHaveBeenCalledWith(OTHER_TASK_ID, null);
  });
});
