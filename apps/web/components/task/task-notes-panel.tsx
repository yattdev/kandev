"use client";

import { memo, useState, useCallback, useEffect, useRef } from "react";
import { PanelRoot, PanelBody } from "./panel-primitives";
import dynamic from "@/lib/routing/client-dynamic";
import { IconLoader2, IconNote } from "@tabler/icons-react";
import { useTaskNotes } from "@/hooks/domains/session/use-task-notes";
import { useAppStore } from "@/components/state-provider";

const PlanEditor = dynamic(
  () =>
    import("@/components/editors/tiptap/tiptap-plan-editor").then((mod) => mod.TipTapPlanEditor),
  {
    ssr: false,
    loading: () => (
      <div className="flex h-full items-center justify-center text-muted-foreground text-sm">
        Loading editor...
      </div>
    ),
  },
);

/** Debounce delay for auto-saving notes content (ms) */
const AUTO_SAVE_DELAY = 1500;

type TaskNotesPanelProps = {
  taskId: string | null;
  visible?: boolean;
};

function useNotesDraft(
  notes: { content?: string } | null | undefined,
  isSaving: boolean,
  saveNotes: (content: string) => Promise<unknown>,
  editorWrapperRef: React.RefObject<HTMLDivElement | null>,
) {
  const [draftContent, setDraftContent] = useState(notes?.content ?? "");
  const draftContentRef = useRef(draftContent);
  const [editorKey, setEditorKey] = useState(0);
  const lastNotesContentRef = useRef<string | undefined>(undefined);
  const isExternalUpdateRef = useRef(false);
  const [isEditorFocused, setIsEditorFocused] = useState(false);
  const autoSaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const handleEmptyStateClick = useCallback(() => {
    const el = editorWrapperRef.current?.querySelector(".ProseMirror");
    if (el) (el as HTMLElement).focus();
  }, [editorWrapperRef]);

  useEffect(() => {
    const checkFocus = () => {
      const wrapper = editorWrapperRef.current;
      if (!wrapper) return;
      setIsEditorFocused(wrapper.contains(document.activeElement));
    };
    document.addEventListener("focusin", checkFocus);
    document.addEventListener("focusout", checkFocus);
    checkFocus();
    return () => {
      document.removeEventListener("focusin", checkFocus);
      document.removeEventListener("focusout", checkFocus);
    };
  }, [editorWrapperRef]);

  useEffect(() => {
    draftContentRef.current = draftContent;
  }, [draftContent]);

  useEffect(() => {
    const prevContent = lastNotesContentRef.current;
    const newContent = notes?.content;
    lastNotesContentRef.current = newContent;
    if (newContent !== prevContent) {
      const resolved = newContent ?? "";
      if (resolved === draftContentRef.current) return;
      isExternalUpdateRef.current = true;
      // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing external notes data to local editor state
      setDraftContent(resolved);
      setEditorKey((k) => k + 1);
    }
  }, [notes?.content]);

  useEffect(() => {
    if (isExternalUpdateRef.current) {
      isExternalUpdateRef.current = false;
      return;
    }
    const hasChanges = notes ? draftContent !== notes.content : draftContent.length > 0;
    if (!hasChanges || isSaving) return;
    if (autoSaveTimerRef.current) clearTimeout(autoSaveTimerRef.current);
    autoSaveTimerRef.current = setTimeout(() => {
      autoSaveTimerRef.current = null;
      saveNotes(draftContent);
    }, AUTO_SAVE_DELAY);
    return () => {
      if (autoSaveTimerRef.current) {
        clearTimeout(autoSaveTimerRef.current);
        autoSaveTimerRef.current = null;
      }
    };
  }, [draftContent, notes, isSaving, saveNotes]);

  const hasUnsavedChanges = notes ? draftContent !== notes.content : draftContent.length > 0;
  return {
    draftContent,
    setDraftContent,
    editorKey,
    isEditorFocused,
    handleEmptyStateClick,
    hasUnsavedChanges,
  };
}

function useSaveShortcut(
  hasUnsavedChanges: boolean,
  isSaving: boolean,
  saveNotes: (content: string) => Promise<unknown>,
  draftContent: string,
) {
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "s") {
        e.preventDefault();
        if (hasUnsavedChanges && !isSaving) saveNotes(draftContent);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [hasUnsavedChanges, isSaving, saveNotes, draftContent]);
}

export const TaskNotesPanel = memo(function TaskNotesPanel({
  taskId,
  visible = true,
}: TaskNotesPanelProps) {
  const { notes, isLoading, isSaving, saveNotes } = useTaskNotes(taskId, { visible });
  const activeTaskId = useAppStore((s) => s.tasks.activeTaskId);
  const editorWrapperRef = useRef<HTMLDivElement>(null);

  const { draftContent, setDraftContent, editorKey, isEditorFocused, handleEmptyStateClick } =
    useNotesDraft(notes, isSaving, saveNotes, editorWrapperRef);

  useSaveShortcut(
    notes ? draftContent !== notes.content : draftContent.length > 0,
    isSaving,
    saveNotes,
    draftContent,
  );

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <IconLoader2 className="h-5 w-5 animate-spin mr-2" />
        <span className="text-sm">Loading notes...</span>
      </div>
    );
  }

  if (!taskId || !activeTaskId) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <span className="text-sm">No task selected</span>
      </div>
    );
  }

  return (
    <PanelRoot data-testid="notes-panel">
      <PanelBody
        padding={false}
        scroll={false}
        className="relative cursor-text"
        ref={editorWrapperRef}
        onClick={handleEmptyStateClick}
        data-panel-kind="notes"
      >
        <PlanEditor
          key={`${taskId}-notes-${editorKey}`}
          value={draftContent}
          onChange={setDraftContent}
          placeholder="Jot down notes, reminders, or any context for this task..."
        />
        {!isLoading && draftContent.trim() === "" && !isEditorFocused && (
          <div
            className="absolute inset-0 flex items-center justify-center pointer-events-none"
            onClick={handleEmptyStateClick}
          >
            <div className="flex flex-col items-center gap-6 max-w-md px-6">
              <div className="flex items-center justify-center w-12 h-12 rounded-xl bg-muted/50">
                <IconNote className="h-6 w-6 text-muted-foreground" />
              </div>
              <div className="text-center">
                <h3 className="text-sm font-medium text-foreground mb-1">
                  Keep notes for this task
                </h3>
                <p className="text-xs text-muted-foreground">
                  A private scratchpad for reminders, context, and any information related to this
                  task
                </p>
              </div>
              <p className="text-xs text-muted-foreground/70">Click anywhere to start writing</p>
            </div>
          </div>
        )}
      </PanelBody>
    </PanelRoot>
  );
});
