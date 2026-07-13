import { getWebSocketClient } from "@/lib/ws/connection";
import type { TaskNotes } from "@/lib/types/http";

const WS_CLIENT_UNAVAILABLE = "WebSocket client not available";

/**
 * Get the task notes for a specific task.
 * Returns null if no notes exist yet.
 */
export async function getTaskNotes(taskId: string): Promise<TaskNotes | null> {
  const client = getWebSocketClient();
  if (!client) {
    throw new Error(WS_CLIENT_UNAVAILABLE);
  }
  const response = await client.request("task.notes.get", { task_id: taskId });

  if (!response || Object.keys(response).length === 0) {
    return null;
  }

  return response as TaskNotes;
}

/**
 * Save (create or update) task notes for a specific task.
 */
export async function saveTaskNotes(taskId: string, content: string): Promise<TaskNotes> {
  const client = getWebSocketClient();
  if (!client) {
    throw new Error(WS_CLIENT_UNAVAILABLE);
  }
  const response = await client.request("task.notes.save", {
    task_id: taskId,
    content,
  });

  return response as TaskNotes;
}

/**
 * Delete the task notes for a specific task.
 */
export async function deleteTaskNotes(taskId: string): Promise<void> {
  const client = getWebSocketClient();
  if (!client) {
    throw new Error(WS_CLIENT_UNAVAILABLE);
  }
  await client.request("task.notes.delete", { task_id: taskId });
}
