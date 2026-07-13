import type { StoreApi } from "zustand";
import type { AppState } from "@/lib/state/store";
import type { WsHandlers } from "@/lib/ws/handlers/types";

export function registerTaskNotesHandlers(store: StoreApi<AppState>): WsHandlers {
  return {
    "task.notes.updated": (message) => {
      const notes = message.payload;
      store.getState().setTaskNotes(notes.task_id, notes);
    },
    "task.notes.deleted": (message) => {
      const { task_id } = message.payload;
      store.getState().setTaskNotes(task_id, null);
    },
  };
}
