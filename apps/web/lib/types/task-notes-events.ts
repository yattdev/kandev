import type { TaskNotes } from "@/lib/types/http";

export type TaskNotesEventPayload = TaskNotes;

export type TaskNotesDeletedPayload = {
  task_id: string;
};
