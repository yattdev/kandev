import { afterEach, describe, expect, it } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import type { DockviewApi } from "dockview-react";
import { StateProvider, useAppStoreApi } from "@/components/state-provider";
import { defaultState } from "@/lib/state/default-state";
import { useDockviewStore } from "@/lib/state/dockview-store";
import type { TaskSession, TaskId } from "@/lib/types/http";
import { makeReorderingAutoSessionApi } from "./dockview-session-tabs.test-utils";
import { useAutoSessionTab } from "./dockview-session-tabs";

const TASK_ID = "task-1" as TaskId;
const ACTIVE_SESSION_ID = "session-a";
const SIBLING_SESSION_ID = "session-b";
let appStore: ReturnType<typeof useAppStoreApi> | null = null;

function Harness() {
  appStore = useAppStoreApi();
  useAutoSessionTab(ACTIVE_SESSION_ID);
  return null;
}

function renderHookWithHydratedSessions() {
  const sessions = [ACTIVE_SESSION_ID, SIBLING_SESSION_ID].map((id) => ({ id }) as TaskSession);

  return render(
    <StateProvider
      initialState={{
        ...defaultState,
        tasks: {
          ...defaultState.tasks,
          activeTaskId: TASK_ID,
          activeSessionId: ACTIVE_SESSION_ID,
        },
        taskSessionsByTask: {
          ...defaultState.taskSessionsByTask,
          itemsByTaskId: { [TASK_ID]: sessions },
        },
      }}
    >
      <Harness />
    </StateProvider>,
  );
}

function renderHookWithTerminalHistory() {
  const sessions = [
    {
      id: ACTIVE_SESSION_ID,
      task_id: TASK_ID,
      state: "WAITING_FOR_INPUT",
      is_primary: true,
    } as TaskSession,
    {
      id: SIBLING_SESSION_ID,
      task_id: TASK_ID,
      state: "COMPLETED",
      is_primary: false,
    } as TaskSession,
  ];

  return render(
    <StateProvider
      initialState={{
        ...defaultState,
        tasks: {
          ...defaultState.tasks,
          activeTaskId: TASK_ID,
          activeSessionId: ACTIVE_SESSION_ID,
        },
        taskSessionsByTask: {
          ...defaultState.taskSessionsByTask,
          itemsByTaskId: { [TASK_ID]: sessions },
        },
      }}
    >
      <Harness />
    </StateProvider>,
  );
}

function renderHookWithRunningHelper() {
  const sessions = [
    {
      id: ACTIVE_SESSION_ID,
      task_id: TASK_ID,
      state: "WAITING_FOR_INPUT",
      is_primary: true,
    } as TaskSession,
    {
      id: SIBLING_SESSION_ID,
      task_id: TASK_ID,
      state: "RUNNING",
      is_primary: false,
    } as TaskSession,
  ];

  return render(
    <StateProvider
      initialState={{
        ...defaultState,
        tasks: {
          ...defaultState.tasks,
          activeTaskId: TASK_ID,
          activeSessionId: ACTIVE_SESSION_ID,
        },
        taskSessionsByTask: {
          ...defaultState.taskSessionsByTask,
          itemsByTaskId: { [TASK_ID]: sessions },
        },
      }}
    >
      <Harness />
    </StateProvider>,
  );
}

afterEach(() => {
  cleanup();
  appStore = null;
  useDockviewStore.setState({ api: null, sessionHistoryVisibleByTaskId: {} });
});

describe("useAutoSessionTab", () => {
  it("reconciles every hydrated session when Dockview becomes ready later", () => {
    // @covers AC-UI-TASK-AGENT-TAB-RECONCILIATION-001.1
    useDockviewStore.setState({ api: null });
    renderHookWithHydratedSessions();

    const { api } = makeReorderingAutoSessionApi();
    act(() => {
      useDockviewStore.setState({ api: api as DockviewApi });
    });

    expect(api.panels.map((panel) => panel.id)).toEqual(
      expect.arrayContaining([`session:${ACTIVE_SESSION_ID}`, `session:${SIBLING_SESSION_ID}`]),
    );
  });

  it("hides terminal helper panels until desktop history is explicitly shown", () => {
    const { api } = makeReorderingAutoSessionApi();
    useDockviewStore.setState({ api: api as DockviewApi, sessionHistoryVisibleByTaskId: {} });
    renderHookWithTerminalHistory();

    expect(api.panels.map((panel) => panel.id)).toContain(`session:${ACTIVE_SESSION_ID}`);
    expect(api.panels.map((panel) => panel.id)).not.toContain(`session:${SIBLING_SESSION_ID}`);

    act(() => {
      useDockviewStore.getState().setSessionHistoryVisible(TASK_ID, true);
    });
    expect(api.panels.map((panel) => panel.id)).toContain(`session:${ACTIVE_SESSION_ID}`);
    expect(api.panels.map((panel) => panel.id)).toContain(`session:${SIBLING_SESSION_ID}`);

    act(() => {
      useDockviewStore.getState().setSessionHistoryVisible(TASK_ID, false);
    });
    expect(api.panels.map((panel) => panel.id)).not.toContain(`session:${SIBLING_SESSION_ID}`);
    expect(api.panels.map((panel) => panel.id)).toContain(`session:${ACTIVE_SESSION_ID}`);
  });

  it("closes a helper panel when its state becomes terminal", () => {
    const { api } = makeReorderingAutoSessionApi();
    useDockviewStore.setState({ api: api as DockviewApi, sessionHistoryVisibleByTaskId: {} });
    renderHookWithRunningHelper();

    expect(api.panels.map((panel) => panel.id)).toContain(`session:${SIBLING_SESSION_ID}`);

    act(() => {
      const store = appStore;
      if (!store) throw new Error("app store was not captured");
      store.setState((state) => ({
        taskSessionsByTask: {
          ...state.taskSessionsByTask,
          itemsByTaskId: {
            ...state.taskSessionsByTask.itemsByTaskId,
            [TASK_ID]: state.taskSessionsByTask.itemsByTaskId[TASK_ID].map((session) =>
              session.id === SIBLING_SESSION_ID ? { ...session, state: "COMPLETED" } : session,
            ),
          },
        },
      }));
    });

    expect(api.panels.map((panel) => panel.id)).not.toContain(`session:${SIBLING_SESSION_ID}`);
    expect(api.panels.map((panel) => panel.id)).toContain(`session:${ACTIVE_SESSION_ID}`);
  });
});
