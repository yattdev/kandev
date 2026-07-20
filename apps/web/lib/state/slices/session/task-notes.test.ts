import { describe, it, expect, beforeEach } from "vitest";
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";
import { createSessionSlice } from "./session-slice";
import type { SessionSlice } from "./types";
import type { TaskNotes } from "@/lib/types/http-agents";

function makeStore() {
  return create<SessionSlice>()(immer(createSessionSlice));
}

const TASK_ID = "task-1";

function makeNotes(overrides: Partial<TaskNotes> = {}): TaskNotes {
  return {
    id: "notes-1",
    task_id: TASK_ID,
    content: "# Notes",
    author_kind: "user",
    author_name: "Test User",
    created_at: "2026-04-20T00:00:00Z",
    updated_at: "2026-04-20T00:00:00Z",
    ...overrides,
  } as TaskNotes;
}

describe("task notes slice", () => {
  beforeEach(() => {});

  it("initial state has empty byTaskId", () => {
    const store = makeStore();
    expect(store.getState().taskNotes.byTaskId).toEqual({});
  });

  it("setTaskNotes stores notes and marks loaded", () => {
    const store = makeStore();
    const notes = makeNotes();

    store.getState().setTaskNotes(TASK_ID, notes);

    expect(store.getState().taskNotes.byTaskId[TASK_ID]).toEqual(notes);
    expect(store.getState().taskNotes.loadedByTaskId[TASK_ID]).toBe(true);
    expect(store.getState().taskNotes.loadingByTaskId[TASK_ID]).toBe(false);
  });

  it("setTaskNotes with null stores null and marks loaded", () => {
    const store = makeStore();
    store.getState().setTaskNotes(TASK_ID, makeNotes());
    store.getState().setTaskNotes(TASK_ID, null);

    expect(store.getState().taskNotes.byTaskId[TASK_ID]).toBeNull();
    expect(store.getState().taskNotes.loadedByTaskId[TASK_ID]).toBe(true);
  });

  it("setTaskNotesLoading sets loading flag", () => {
    const store = makeStore();

    store.getState().setTaskNotesLoading(TASK_ID, true);
    expect(store.getState().taskNotes.loadingByTaskId[TASK_ID]).toBe(true);

    store.getState().setTaskNotesLoading(TASK_ID, false);
    expect(store.getState().taskNotes.loadingByTaskId[TASK_ID]).toBe(false);
  });

  it("setTaskNotesSaving sets saving flag", () => {
    const store = makeStore();

    store.getState().setTaskNotesSaving(TASK_ID, true);
    expect(store.getState().taskNotes.savingByTaskId[TASK_ID]).toBe(true);

    store.getState().setTaskNotesSaving(TASK_ID, false);
    expect(store.getState().taskNotes.savingByTaskId[TASK_ID]).toBe(false);
  });
});
