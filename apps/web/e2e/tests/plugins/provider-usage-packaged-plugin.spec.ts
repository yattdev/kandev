import path from "node:path";
import { test, expect } from "../../fixtures/office-fixture";
import { dwell } from "../../helpers/causal-waits";
import { SessionPage } from "../../pages/session-page";

const PLUGIN_ID = "kandev-provider-usage";
const TOOL_NAME = "kandev_kandev_provider_usage_get_provider_usage";

const packagePath = process.env.KANDEV_PROVIDER_USAGE_PLUGIN_PACKAGE?.trim();

test.skip(
  !packagePath,
  "requires KANDEV_PROVIDER_USAGE_PLUGIN_PACKAGE from the attached plugin repo",
);

// Mirrors docs/specs/plugins/provider-usage-agent-tool.md's v1 response
// shape. Kept loose (not every optional field) because this spec asserts
// structural/contract invariants, not the plugin's internal provider state
// on whatever machine CI happens to run on.
interface ProviderUsageWindow {
  window_id: string;
  window_name: string;
  window_seconds: number | null;
  scoped: boolean;
  utilization_percentage: number;
  remaining_percentage?: number;
  reset_at: string | null;
  availability_state: string;
}

interface ProviderUsageProvider {
  provider_id: string;
  provider_name: string;
  account: unknown;
  support_state: "supported" | "unsupported" | "unknown";
  availability_state:
    | "available"
    | "quota_exhausted"
    | "provider_unavailable"
    | "telemetry_stale"
    | "not_configured"
    | "unsupported"
    | "unknown";
  fetched_at: string;
  age_seconds: number;
  stale: boolean;
  windows: ProviderUsageWindow[];
  reason?: { code: string; retryable: boolean; user_action_required: boolean; reset_at?: string };
  warnings?: string[];
}

interface ProviderUsageResult {
  schema_version: string;
  evaluated_at: string;
  snapshot_generated_at: string | null;
  poll_interval_seconds: number;
  stale_after_seconds: number;
  partial: boolean;
  scope: { usage_scope: string; user_scoped: boolean; invocation_workspace_id: string };
  providers: ProviderUsageProvider[];
}

const AVAILABILITY_STATES = new Set([
  "available",
  "quota_exhausted",
  "provider_unavailable",
  "telemetry_stale",
  "not_configured",
  "unsupported",
  "unknown",
]);

function mcpCallScript(argsJson: string): string {
  return [
    'e2e:thinking("Reading provider capacity before routing...")',
    "e2e:delay(50)",
    `e2e:mcp:kandev:${TOOL_NAME}(${argsJson})`,
    "e2e:delay(50)",
    'e2e:message("Done.")',
  ].join("\n");
}

async function installPackagedPlugin(testPage: import("@playwright/test").Page): Promise<void> {
  if (!packagePath) throw new Error("Provider Usage plugin package path is required");
  await testPage.goto("/settings/plugins");
  await testPage.getByTestId("install-plugin-trigger").click();
  await testPage.getByTestId("install-plugin-tab-upload").click();
  await testPage.getByTestId("install-plugin-file-input").setInputFiles(path.resolve(packagePath));
  await testPage.getByTestId("install-plugin-upload-submit").click();
  const pluginRow = testPage.getByTestId(`plugin-row-${PLUGIN_ID}`);
  await expect(pluginRow).toBeVisible({ timeout: 15_000 });
  await expect(pluginRow.getByText("Active", { exact: true })).toBeVisible();
}

/**
 * Reads back the tool call's structured content from persisted session
 * messages. The mock agent completes the MCP tool call with ACP kind
 * "other", which the Kandev adapter normalizes as a generic tool payload
 * stored at `message.metadata.normalized.generic.output` — `result` is the
 * tool's fallback text, `structuredContent` is its JSON structured content
 * (present only when the tool actually returned one), `error` is present
 * only when the call failed (e.g. schema validation rejected it).
 */
async function waitForToolCallOutput(
  apiClient: {
    listSessionMessages: (sessionId: string) => Promise<{
      messages: Array<{ content: string; metadata?: Record<string, unknown> }>;
    }>;
  },
  sessionId: string,
): Promise<{ result?: string; structuredContent?: unknown; error?: string }> {
  let output: { result?: string; structuredContent?: unknown; error?: string } | undefined;
  await expect
    .poll(
      async () => {
        const { messages } = await apiClient.listSessionMessages(sessionId);
        const toolCall = messages.find((message) => message.content === TOOL_NAME);
        const normalized = toolCall?.metadata?.normalized;
        if (!normalized || typeof normalized !== "object") return false;
        const generic = (normalized as { generic?: { output?: unknown } }).generic;
        if (!generic?.output || typeof generic.output !== "object") return false;
        output = generic.output as typeof output;
        return true;
      },
      { timeout: 30_000, message: `MCP tool call for ${TOOL_NAME} was not persisted` },
    )
    .toBe(true);
  if (!output) throw new Error("unreachable: expect.poll resolved true without setting output");
  return output;
}

function assertWellFormedResult(result: ProviderUsageResult, expectedWorkspaceId: string): void {
  expect(result.schema_version).toBe("1");
  expect(typeof result.evaluated_at).toBe("string");
  expect(result.poll_interval_seconds).toBeGreaterThanOrEqual(60);
  expect(result.stale_after_seconds).toBe(result.poll_interval_seconds * 2);
  expect(typeof result.partial).toBe("boolean");
  expect(result.scope).toEqual({
    usage_scope: "instance",
    user_scoped: false,
    invocation_workspace_id: expectedWorkspaceId,
  });
  expect(Array.isArray(result.providers)).toBe(true);
  for (const provider of result.providers) {
    expect(typeof provider.provider_id).toBe("string");
    expect(provider.provider_id.length).toBeGreaterThan(0);
    expect(["supported", "unsupported", "unknown"]).toContain(provider.support_state);
    expect(AVAILABILITY_STATES.has(provider.availability_state)).toBe(true);
    expect(provider.age_seconds).toBeGreaterThanOrEqual(0);
    expect(provider.stale).toBe(provider.age_seconds >= result.stale_after_seconds);
    expect(Array.isArray(provider.windows)).toBe(true);
    for (const window of provider.windows) {
      expect(AVAILABILITY_STATES.has(window.availability_state)).toBe(true);
      expect(window.utilization_percentage).toBeGreaterThanOrEqual(0);
      expect(window.utilization_percentage).toBeLessThanOrEqual(100);
    }
    // Never present, in any state: raw credential/account secrets.
    const serialized = JSON.stringify(provider);
    expect(serialized).not.toMatch(/bearer |authorization|api[_-]?key|secret|password/i);
  }
}

test.describe("Provider Usage packaged plugin — agent tool", () => {
  test.afterEach(async ({ apiClient }) => {
    await apiClient.rawRequest("DELETE", `/api/plugins/${PLUGIN_ID}`).catch(() => undefined);
  });

  test("a kanban task agent discovers and calls the tool, receiving the v1 structured payload", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(150_000);
    await installPackagedPlugin(testPage);

    // First call: the plugin may still be on its immediate startup
    // partial/unknown response (background poll hasn't completed yet). A
    // successful call at all is proof the tool was discovered on the
    // kanban-task surface — an undiscovered/unavailable tool fails the
    // script's MCP call instead of returning a well-formed result.
    const firstTask = await apiClient.createTaskWithAgent(
      seedData.workspaceId,
      "Provider Usage MCP E2E — kanban first call",
      seedData.agentProfileId,
      {
        description: mcpCallScript("{}"),
        workflow_id: seedData.workflowId,
        workflow_step_id: seedData.startStepId,
        repository_ids: [seedData.repositoryId],
      },
    );
    await testPage.goto(`/t/${firstTask.id}`);
    const firstSession = new SessionPage(testPage);
    await firstSession.waitForLoad();
    await firstSession.waitForChatIdle({ timeout: 60_000 });
    const { sessions: firstSessions } = await apiClient.listTaskSessions(firstTask.id);
    const firstOutput = await waitForToolCallOutput(apiClient, firstSessions[0].id);
    expect(firstOutput.error).toBeUndefined();
    const firstResult = firstOutput.structuredContent as ProviderUsageResult;
    assertWellFormedResult(firstResult, seedData.workspaceId);

    // The plugin's poller runs on its own configured interval (minimum 60s)
    // with nothing client-observable to hook a wait on, so this is a real
    // dwell rather than a disguised sleep for something else.
    await dwell(
      testPage,
      Math.max(firstResult.poll_interval_seconds, 60) * 1000 + 5_000,
      "poll-interval",
      "waiting for the plugin's background provider poller (minimum 60s interval) to complete at least one cycle",
    );

    const secondTask = await apiClient.createTaskWithAgent(
      seedData.workspaceId,
      "Provider Usage MCP E2E — kanban second call",
      seedData.agentProfileId,
      {
        description: mcpCallScript("{}"),
        workflow_id: seedData.workflowId,
        workflow_step_id: seedData.startStepId,
        repository_ids: [seedData.repositoryId],
      },
    );
    await testPage.goto(`/t/${secondTask.id}`);
    const secondSession = new SessionPage(testPage);
    await secondSession.waitForLoad();
    await secondSession.waitForChatIdle({ timeout: 60_000 });
    const { sessions: secondSessions } = await apiClient.listTaskSessions(secondTask.id);
    const secondOutput = await waitForToolCallOutput(apiClient, secondSessions[0].id);
    const secondResult = secondOutput.structuredContent as ProviderUsageResult;
    assertWellFormedResult(secondResult, seedData.workspaceId);
    // After the first real poll, the snapshot must be populated.
    expect(secondResult.snapshot_generated_at).not.toBeNull();
  });

  test("an office task agent (the coordinator surface) can also discover and call the tool", async ({
    testPage,
    apiClient,
    officeSeed,
  }) => {
    test.setTimeout(90_000);
    await installPackagedPlugin(testPage);

    const task = await apiClient.createTaskWithAgent(
      officeSeed.workspaceId,
      "Provider Usage MCP E2E — office coordinator",
      officeSeed.agentId,
      {
        description: mcpCallScript("{}"),
        workflow_id: officeSeed.workflowId,
      },
    );
    await testPage.goto(`/t/${task.id}`);
    const session = new SessionPage(testPage);
    await session.waitForLoad();
    await session.waitForChatIdle({ timeout: 60_000 });
    const { sessions } = await apiClient.listTaskSessions(task.id);
    const output = await waitForToolCallOutput(apiClient, sessions[0].id);
    expect(output.error).toBeUndefined();
    const result = output.structuredContent as ProviderUsageResult;
    assertWellFormedResult(result, officeSeed.workspaceId);
  });

  test("a call carrying an unexpected property is rejected before the plugin is invoked", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(90_000);
    await installPackagedPlugin(testPage);

    const task = await apiClient.createTaskWithAgent(
      seedData.workspaceId,
      "Provider Usage MCP E2E — schema rejection",
      seedData.agentProfileId,
      {
        description: mcpCallScript('{"unexpected_argument":"spoofed-value"}'),
        workflow_id: seedData.workflowId,
        workflow_step_id: seedData.startStepId,
        repository_ids: [seedData.repositoryId],
      },
    );
    await testPage.goto(`/t/${task.id}`);
    const session = new SessionPage(testPage);
    await session.waitForLoad();
    await session.waitForChatIdle({ timeout: 60_000 });
    const { sessions } = await apiClient.listTaskSessions(task.id);
    const output = await waitForToolCallOutput(apiClient, sessions[0].id);
    // additionalProperties: false on the tool's empty input schema means the
    // host's generic MCP schema validation rejects this before the plugin
    // ever sees the call — never a structured/successful result.
    expect(output.structuredContent).toBeUndefined();
    expect(typeof output.error).toBe("string");
    expect((output.error ?? "").length).toBeGreaterThan(0);
  });

  test("the tool is absent from the External MCP surface", async ({ testPage, backend }) => {
    void testPage;
    const initRes = await fetch(`${backend.baseUrl}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "initialize",
        params: {
          protocolVersion: "2025-06-18",
          clientInfo: { name: "provider-usage-e2e", version: "1.0" },
          capabilities: {},
        },
      }),
    });
    expect(initRes.status, await initRes.text().catch(() => "")).toBe(200);
    const sessionId = initRes.headers.get("Mcp-Session-Id");
    expect(sessionId, "External MCP endpoint did not return a session id").toBeTruthy();

    const listRes = await fetch(`${backend.baseUrl}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        "Mcp-Session-Id": sessionId ?? "",
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list", params: {} }),
    });
    expect(listRes.status, await listRes.text().catch(() => "")).toBe(200);
    const body = (await listRes.json()) as { result?: { tools?: Array<{ name: string }> } };
    const toolNames = (body.result?.tools ?? []).map((t) => t.name);
    expect(toolNames).not.toContain(TOOL_NAME);
  });
});
