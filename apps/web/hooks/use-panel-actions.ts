"use client";

import { useCallback, useMemo } from "react";
import { useResponsiveBreakpoint } from "@/hooks/use-responsive-breakpoint";
import { useDockviewStore } from "@/lib/state/dockview-store";
import { useLayoutStore } from "@/lib/state/layout-store";
import { useAppStore } from "@/components/state-provider";
import { useFileEditors } from "@/hooks/use-file-editors";

function useMobileTabletDocumentActions() {
  const activeSessionId = useAppStore((state) => state.tasks.activeSessionId);
  const openDocument = useLayoutStore((s) => s.openDocument);
  const setActiveDocument = useAppStore((s) => s.setActiveDocument);
  const setMobileSessionPanel = useAppStore((s) => s.setMobileSessionPanel);
  const activeTaskId = useAppStore((s) => s.tasks.activeTaskId);
  const setPlanMode = useAppStore((s) => s.setPlanMode);
  return {
    activeSessionId,
    openDocument,
    setActiveDocument,
    setMobileSessionPanel,
    activeTaskId,
    setPlanMode,
  };
}

function useDesktopPanelActions() {
  const addBrowser = useDockviewStore((s) => s.addBrowserPanel);
  const addPlan = useDockviewStore((s) => s.addPlanPanel);
  const addNotes = useDockviewStore((s) => s.addNotesPanel);
  const addChat = useDockviewStore((s) => s.addChatPanel);
  const addChanges = useDockviewStore((s) => s.addChangesPanel);
  const addTerminal = useDockviewStore((s) => s.addTerminalPanel);
  const addVscode = useDockviewStore((s) => s.addVscodePanel);
  const { openFile, openFileInMarkdownPreview } = useFileEditors();

  return useMemo(
    () => ({
      addBrowser,
      addPlan,
      addNotes,
      addChat,
      addChanges,
      addTerminal,
      addVscode,
      openFile,
      openFileInMarkdownPreview,
    }),
    [
      addBrowser,
      addPlan,
      addNotes,
      addChat,
      addChanges,
      addTerminal,
      addVscode,
      openFile,
      openFileInMarkdownPreview,
    ],
  );
}

/**
 * Unified hook returning add-only panel action functions.
 * On desktop: delegates to dockview store.
 * On mobile/tablet: delegates to layout store (kept for backward compat).
 */
export function usePanelActions() {
  const { usesDesktopWorkbench, isMobile } = useResponsiveBreakpoint();

  const desktopActions = useDesktopPanelActions();
  const {
    activeSessionId,
    openDocument,
    setActiveDocument,
    setMobileSessionPanel,
    activeTaskId,
    setPlanMode,
  } = useMobileTabletDocumentActions();

  const addBrowser = useCallback(
    (url?: string) => {
      if (usesDesktopWorkbench) {
        desktopActions.addBrowser(url);
      } else if (activeSessionId) {
        useLayoutStore.getState().openPreview(activeSessionId);
      }
    },
    [usesDesktopWorkbench, desktopActions, activeSessionId],
  );

  const addPlan = useCallback(() => {
    if (usesDesktopWorkbench) {
      desktopActions.addPlan();
    } else if (activeSessionId && activeTaskId) {
      setActiveDocument(activeSessionId, { type: "plan", taskId: activeTaskId });
      openDocument(activeSessionId);
      setPlanMode(activeSessionId, true);
    }
  }, [
    usesDesktopWorkbench,
    desktopActions,
    activeSessionId,
    activeTaskId,
    setActiveDocument,
    openDocument,
    setPlanMode,
  ]);

  const addNotes = useCallback(() => {
    if (usesDesktopWorkbench) {
      desktopActions.addNotes();
    } else if (isMobile && activeSessionId) {
      setMobileSessionPanel(activeSessionId, "notes");
    } else if (activeSessionId && activeTaskId) {
      setActiveDocument(activeSessionId, { type: "notes", taskId: activeTaskId });
      openDocument(activeSessionId);
    }
  }, [
    usesDesktopWorkbench,
    isMobile,
    desktopActions,
    activeSessionId,
    activeTaskId,
    setMobileSessionPanel,
    setActiveDocument,
    openDocument,
  ]);

  const addChat = useCallback(() => {
    if (usesDesktopWorkbench) desktopActions.addChat();
  }, [usesDesktopWorkbench, desktopActions]);

  const addChanges = useCallback(() => {
    if (usesDesktopWorkbench) desktopActions.addChanges();
  }, [usesDesktopWorkbench, desktopActions]);

  const addTerminal = useCallback(
    (terminalId?: string) => {
      if (usesDesktopWorkbench) desktopActions.addTerminal(terminalId);
    },
    [usesDesktopWorkbench, desktopActions],
  );

  const addVscode = useCallback(() => {
    if (usesDesktopWorkbench) desktopActions.addVscode();
  }, [usesDesktopWorkbench, desktopActions]);

  const openFile = useCallback(
    (filePath: string, repo?: string) => {
      if (usesDesktopWorkbench) desktopActions.openFile(filePath, repo);
    },
    [usesDesktopWorkbench, desktopActions],
  );

  const openFileInMarkdownPreview = useCallback(
    (filePath: string, repo?: string) => {
      if (usesDesktopWorkbench) desktopActions.openFileInMarkdownPreview(filePath, repo);
    },
    [usesDesktopWorkbench, desktopActions],
  );

  return {
    addBrowser,
    addPlan,
    addNotes,
    addChat,
    addChanges,
    addTerminal,
    addVscode,
    openFile,
    openFileInMarkdownPreview,
  };
}
