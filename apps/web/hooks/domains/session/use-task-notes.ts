import { useEffect, useCallback, useState, useRef } from "react";
import { useAppStore } from "@/components/state-provider";
import { getTaskNotes, saveTaskNotes, deleteTaskNotes } from "@/lib/api/domains/notes-api";
import type { TaskNotes } from "@/lib/types/http";

/**
 * Hook to fetch and manage the notes for a task.
 * Notes are task-scoped (one notes document per task).
 * @param taskId - The task ID to fetch notes for
 * @param options.visible - When true, refetches on tab focus
 */
export function useTaskNotes(taskId: string | null, options?: { visible?: boolean }) {
  const { visible = true } = options ?? {};
  const prevVisibleRef = useRef(visible);

  const notes = useAppStore((state) =>
    taskId ? state.taskNotes.byTaskId[taskId] : undefined,
  );
  const isLoading = useAppStore((state) =>
    taskId ? (state.taskNotes.loadingByTaskId[taskId] ?? false) : false,
  );
  const isLoaded = useAppStore((state) =>
    taskId ? (state.taskNotes.loadedByTaskId[taskId] ?? false) : false,
  );
  const isSaving = useAppStore((state) =>
    taskId ? (state.taskNotes.savingByTaskId[taskId] ?? false) : false,
  );
  const setTaskNotes = useAppStore((state) => state.setTaskNotes);
  const setTaskNotesLoading = useAppStore((state) => state.setTaskNotesLoading);
  const setTaskNotesSaving = useAppStore((state) => state.setTaskNotesSaving);
  const connectionStatus = useAppStore((state) => state.connection.status);

  const [error, setError] = useState<string | null>(null);

  const fetchNotes = useCallback(async () => {
    if (!taskId) return;

    setTaskNotesLoading(taskId, true);
    setError(null);
    try {
      const fetchedNotes = await getTaskNotes(taskId);
      setTaskNotes(taskId, fetchedNotes);
    } catch (err) {
      console.error("Failed to fetch task notes:", err);
      setError(err instanceof Error ? err.message : "Failed to fetch notes");
    } finally {
      setTaskNotesLoading(taskId, false);
    }
  }, [taskId, setTaskNotes, setTaskNotesLoading]);

  // Fetch notes on mount or when taskId changes
  useEffect(() => {
    if (connectionStatus !== "connected") return;
    if (taskId && !isLoaded && !isLoading) {
      fetchNotes();
    }
  }, [taskId, isLoaded, isLoading, fetchNotes, connectionStatus]);

  // Refetch when becoming visible (e.g., tab switch)
  useEffect(() => {
    const wasHidden = !prevVisibleRef.current;
    const isNowVisible = visible;
    prevVisibleRef.current = visible;

    if (wasHidden && isNowVisible && connectionStatus === "connected" && taskId) {
      fetchNotes();
    }
  }, [visible, connectionStatus, taskId, fetchNotes]);

  const saveNotes = useCallback(
    async (content: string): Promise<TaskNotes | null> => {
      if (!taskId) return null;

      setTaskNotesSaving(taskId, true);
      setError(null);
      try {
        const saved = await saveTaskNotes(taskId, content);
        setTaskNotes(taskId, saved);
        return saved;
      } catch (err) {
        console.error("Failed to save task notes:", err);
        setError(err instanceof Error ? err.message : "Failed to save notes");
        return null;
      } finally {
        setTaskNotesSaving(taskId, false);
      }
    },
    [taskId, setTaskNotes, setTaskNotesSaving],
  );

  const removeNotes = useCallback(async (): Promise<boolean> => {
    if (!taskId) return false;

    setTaskNotesSaving(taskId, true);
    setError(null);
    try {
      await deleteTaskNotes(taskId);
      setTaskNotes(taskId, null);
      return true;
    } catch (err) {
      console.error("Failed to delete task notes:", err);
      setError(err instanceof Error ? err.message : "Failed to delete notes");
      return false;
    } finally {
      setTaskNotesSaving(taskId, false);
    }
  }, [taskId, setTaskNotes, setTaskNotesSaving]);

  return {
    notes: notes ?? null,
    isLoading,
    isSaving,
    error,
    saveNotes,
    deleteNotes: removeNotes,
    refetch: fetchNotes,
  };
}
