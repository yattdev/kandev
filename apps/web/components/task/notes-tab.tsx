"use client";

import { DockviewDefaultTab, type IDockviewPanelHeaderProps } from "dockview-react";
import { useTabMaximizeOnDoubleClick } from "./use-tab-maximize";

/**
 * Custom tab component for the Notes panel.
 */
export function NotesTab(props: IDockviewPanelHeaderProps) {
  const { api } = props;
  const onDoubleClick = useTabMaximizeOnDoubleClick(api);

  return (
    <div
      data-testid="notes-tab"
      className="relative cursor-pointer select-none"
      onDoubleClick={onDoubleClick}
    >
      <DockviewDefaultTab {...props} />
    </div>
  );
}
