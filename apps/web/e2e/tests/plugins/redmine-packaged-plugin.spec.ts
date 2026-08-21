import path from "node:path";
import type { Page } from "@playwright/test";
import { expect, test } from "../../fixtures/test-base";
import { KanbanPage } from "../../pages/kanban-page";

// Contract-only coverage for the real packaged Redmine plugin. The package
// comes from its implementation repository; this host test uses only public
// plugin actions and native plugin registrations.
const PLUGIN_ID = "kandev-plugin-redmine";
const packagePath = process.env.KANDEV_REDMINE_PLUGIN_PACKAGE?.trim();
const redmineBaseURL = process.env.KANDEV_REDMINE_E2E_BASE_URL?.trim();
const redmineAPIKey = process.env.KANDEV_REDMINE_E2E_API_KEY?.trim();
const redmineIssueID = Number(process.env.KANDEV_REDMINE_E2E_ISSUE_ID);
const hasLiveRedmine =
  Boolean(packagePath && redmineBaseURL && redmineAPIKey) &&
  Number.isSafeInteger(redmineIssueID) &&
  redmineIssueID > 0;

test.skip(!packagePath, "requires KANDEV_REDMINE_PLUGIN_PACKAGE from the plugin repository");

async function installPackagedPlugin(testPage: Page): Promise<void> {
  if (!packagePath) throw new Error("Redmine plugin package path is required");
  await testPage.goto("/settings/plugins");
  await testPage.getByTestId("install-plugin-trigger").click();
  await testPage.getByTestId("install-plugin-tab-upload").click();
  await testPage.getByTestId("install-plugin-file-input").setInputFiles(path.resolve(packagePath));
  await testPage.getByTestId("install-plugin-upload-submit").click();
  const pluginRow = testPage.getByTestId(`plugin-row-${PLUGIN_ID}`);
  await expect(pluginRow).toBeVisible({ timeout: 15_000 });
  await expect(pluginRow.getByText("Active", { exact: true })).toBeVisible();
}

async function invokePluginAction(
  apiClient: import("../../helpers/api-client").ApiClient,
  key: string,
  workspaceId: string,
  body?: Record<string, unknown>,
  taskId?: string,
): Promise<{ status: number; body: unknown }> {
  const response = await apiClient.rawRequest("POST", `/api/plugins/${PLUGIN_ID}/actions/${key}`, {
    workspaceId,
    ...(taskId ? { taskId } : {}),
    ...(body ? { body } : {}),
  });
  const text = await response.text();
  return { status: response.status, body: text ? JSON.parse(text) : undefined };
}

test.describe("Redmine packaged plugin", () => {
  test.afterEach(async ({ apiClient }) => {
    await apiClient.rawRequest("DELETE", `/api/plugins/${PLUGIN_ID}`).catch(() => undefined);
  });

  // Test-only coverage of behavior already supplied by the packaged plugin.
  test("installs the real package and exposes safe unconfigured defaults", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(60_000);
    await installPackagedPlugin(testPage);

    const connection = await invokePluginAction(apiClient, "connection.get", seedData.workspaceId);
    expect(connection.status).toBe(200);
    expect(connection.body).toEqual({ state: "disconnected" });

    const watches = await invokePluginAction(apiClient, "watches.list", seedData.workspaceId);
    expect(watches.status).toBe(200);
    expect(watches.body).toEqual({ watches: [] });

    const projects = await invokePluginAction(apiClient, "projects.list", seedData.workspaceId);
    expect(projects.status).toBe(200);
    expect(projects.body).toMatchObject({
      error: expect.any(String),
      kind: expect.any(String),
    });
  });

  // Test-only coverage of the host's integration-settings and shared Link
  // dialog contracts using the installed package's real registrations.
  test("renders native settings and Link UI, then revokes and restores them", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(60_000);
    await installPackagedPlugin(testPage);

    await testPage.goto("/settings/integrations/redmine");
    await expect(testPage.getByTestId("redmine-base-url-input")).toBeVisible();
    await expect(testPage.getByTestId("redmine-api-key-input")).toBeVisible();
    const saveButton = testPage.getByTestId("redmine-connection-save");
    await expect(saveButton).toBeDisabled();
    await expect(testPage.locator("#redmine-connection-state")).toHaveText("disconnected");

    await testPage.getByTestId("redmine-base-url-input").fill("https://redmine.example.com");
    await expect(saveButton).toBeDisabled();
    await testPage.getByTestId("redmine-api-key-input").fill("api-key-value");
    await expect(saveButton).toBeEnabled();

    const task = await apiClient.createTask(seedData.workspaceId, "Redmine plugin contract task", {
      workflow_id: seedData.workflowId,
      workflow_step_id: seedData.startStepId,
    });
    const kanban = new KanbanPage(testPage);
    await kanban.goto();
    await kanban.openTaskContextMenu(task.id);
    const linkSubmenu = testPage.getByTestId("task-context-link");
    await linkSubmenu.focus();
    await testPage.keyboard.press("ArrowRight");
    const redmineLink = testPage.getByTestId(`task-context-link-plugin-${PLUGIN_ID}:redmine-link`);
    await expect(redmineLink).toHaveText("Redmine Issue");
    await redmineLink.click();
    const linkDialog = testPage.getByRole("dialog", { name: "Link Redmine issue" });
    await expect(linkDialog).toContainText('Enter a Redmine issue ID, "#123", or an issue URL.');
    await expect(linkDialog.getByTestId("redmine-link-input")).toBeVisible();
    await expect(linkDialog.getByTestId("redmine-link-submit")).toBeVisible();
    await testPage.keyboard.press("Escape");
    await expect(linkDialog).toBeHidden();

    await testPage.goto("/settings/plugins");
    const pluginRow = testPage.getByTestId(`plugin-row-${PLUGIN_ID}`);
    await pluginRow.getByRole("button", { name: "Disable" }).click();
    await expect(pluginRow.getByText("Disabled", { exact: true })).toBeVisible();
    await testPage.goto("/settings/integrations/redmine");
    await expect(testPage.getByTestId("redmine-base-url-input")).toHaveCount(0);

    await testPage.goto("/settings/plugins");
    await pluginRow.getByRole("button", { name: "Enable" }).click();
    await expect(pluginRow.getByText("Active", { exact: true })).toBeVisible();
    await testPage.goto("/settings/integrations/redmine");
    await expect(testPage.getByTestId("redmine-base-url-input")).toBeVisible();
  });

  test("connects, isolates, links, and disconnects against the disposable Redmine service", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.skip(
      !hasLiveRedmine,
      "requires KANDEV_REDMINE_E2E_BASE_URL, KANDEV_REDMINE_E2E_API_KEY, and KANDEV_REDMINE_E2E_ISSUE_ID",
    );
    test.setTimeout(90_000);
    await installPackagedPlugin(testPage);

    const connected = await invokePluginAction(apiClient, "connection.save", seedData.workspaceId, {
      base_url: redmineBaseURL,
      api_key: redmineAPIKey,
    });
    expect(connected.status).toBe(200);
    expect(connected.body).toMatchObject({ state: "connected", base_url: redmineBaseURL });

    const projects = await invokePluginAction(apiClient, "projects.list", seedData.workspaceId);
    expect(projects.status).toBe(200);
    expect(projects.body).toMatchObject({ projects: expect.any(Array), selected_ids: [] });
    expect((projects.body as { projects: unknown[] }).projects.length).toBeGreaterThan(0);

    const task = await apiClient.createTask(
      seedData.workspaceId,
      "Live Redmine plugin contract task",
      {
        workflow_id: seedData.workflowId,
        workflow_step_id: seedData.startStepId,
      },
    );
    const linked = await invokePluginAction(
      apiClient,
      "link.set",
      seedData.workspaceId,
      { reference: `#${redmineIssueID}` },
      task.id,
    );
    expect(linked.status).toBe(200);
    expect(linked.body).toMatchObject({ linked: true, issue_id: redmineIssueID });

    const savedLink = await invokePluginAction(
      apiClient,
      "link.get",
      seedData.workspaceId,
      undefined,
      task.id,
    );
    expect(savedLink.body).toMatchObject({ linked: true, issue_id: redmineIssueID });

    const isolatedWorkspace = await apiClient.createWorkspace("Redmine isolation contract");
    try {
      const isolated = await invokePluginAction(apiClient, "connection.get", isolatedWorkspace.id);
      expect(isolated.status).toBe(200);
      expect(isolated.body).toEqual({ state: "disconnected" });
    } finally {
      await apiClient.deleteWorkspace(isolatedWorkspace.id, isolatedWorkspace.name);
    }

    const unlinked = await invokePluginAction(
      apiClient,
      "link.unset",
      seedData.workspaceId,
      undefined,
      task.id,
    );
    expect(unlinked.body).toEqual({ unlinked: true });

    const disconnected = await invokePluginAction(
      apiClient,
      "connection.disconnect",
      seedData.workspaceId,
    );
    expect(disconnected.body).toEqual({ disconnected: true });
    const connection = await invokePluginAction(apiClient, "connection.get", seedData.workspaceId);
    expect(connection.body).toEqual({ state: "disconnected" });
  });
});
