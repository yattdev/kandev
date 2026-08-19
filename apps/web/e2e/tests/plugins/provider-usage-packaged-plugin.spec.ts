import path from "node:path";
import { test, expect } from "../../fixtures/office-fixture";
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

// The streamable-HTTP MCP endpoint may answer a request as plain JSON or, once
// a session upgrades to SSE (server-decided, not something this raw fetch
// client controls), as an "event: message\ndata: {...}" frame. Handle both
// so this assertion doesn't depend on which transport mode the server chose.
function parseMcpJsonRpcBody(text: string): unknown {
  const dataLine = text
    .split("\n")
    .find((line) => line.startsWith("data:"))
    ?.slice("data:".length)
    .trim();
  return JSON.parse(dataLine ?? text);
}

function mcpCallScript(argsJson: string): string {
  return [
    'e2e:thinking("Reading provider capacity before routing...")',
    "e2e:delay(50)",
    `e2e:mcp:kandev:${TOOL_NAME}(${argsJson})`,
    "e2e:delay(50)",
    'e2e:message("Done.")',
  ].join("\n");
}

async function installPackagedPlugin(
  testPage: import("@playwright/test").Page,
  apiClient: { rawRequest: (method: string, path: string) => Promise<Response> },
): Promise<void> {
  if (!packagePath) throw new Error("Provider Usage plugin package path is required");
  await testPage.goto("/settings/plugins");
  await testPage.getByTestId("install-plugin-trigger").click();
  await testPage.getByTestId("install-plugin-tab-upload").click();
  await testPage.getByTestId("install-plugin-file-input").setInputFiles(path.resolve(packagePath));
  await testPage.getByTestId("install-plugin-upload-submit").click();
  const pluginRow = testPage.getByTestId(`plugin-row-${PLUGIN_ID}`);
  await expect(pluginRow).toBeVisible({ timeout: 15_000 });
  await expect(pluginRow.getByText("Active", { exact: true })).toBeVisible();

  // The store record flips to Active as soon as install validation passes,
  // which can be moments before the supervised subprocess has actually
  // finished starting and is reachable for RPCs (webhook or MCP tool call).
  // Poll the plugin's own status webhook — a real RPC into the live
  // subprocess — until it stops 503ing, so the MCP call below lands after
  // the process (and therefore its agent-tool registration) is genuinely up.
  await expect
    .poll(
      async () => {
        const res = await apiClient.rawRequest("GET", `/api/plugins/${PLUGIN_ID}/webhooks/status`);
        return res.status;
      },
      { timeout: 20_000, message: "Provider Usage plugin subprocess never became reachable" },
    )
    .toBe(200);
}

/**
 * Reads back the tool call's structured content from persisted session
 * messages. The mock agent completes the MCP tool call with ACP kind
 * "other", which the Kandev adapter normalizes as a generic tool payload
 * stored at `message.metadata.normalized.generic.output` — `result` is the
 * tool's fallback text, `structuredContent` is its JSON structured content
 * (present only when the tool actually returned one), `error` is present only
 * when the MCP call itself transport-failed (e.g. an unknown tool name),
 * and `isError` is present when the result is a normal MCP response that
 * signals a tool-level failure (MCP's convention for most rejections,
 * including schema/argument validation — see the MCP spec).
 */
async function waitForToolCallOutput(
  apiClient: {
    listSessionMessages: (sessionId: string) => Promise<{
      messages: Array<{ content: string; metadata?: Record<string, unknown> }>;
    }>;
  },
  sessionId: string,
): Promise<{ result?: string; structuredContent?: unknown; error?: string; isError?: boolean }> {
  let output:
    | { result?: string; structuredContent?: unknown; error?: string; isError?: boolean }
    | undefined;
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
    test.setTimeout(90_000);
    await installPackagedPlugin(testPage, apiClient);

    // A successful call at all is proof the tool was discovered on the
    // kanban-task surface — an undiscovered/unavailable tool fails the
    // script's MCP call instead of returning a well-formed result. The
    // plugin's own unit tests (injected clock) cover the poll/freshness
    // boundary deterministically; waiting out a real multi-minute poll
    // interval here would only duplicate that at large wall-clock cost.
    const task = await apiClient.createTaskWithAgent(
      seedData.workspaceId,
      "Provider Usage MCP E2E — kanban",
      seedData.agentProfileId,
      {
        description: mcpCallScript("{}"),
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
    expect(output.error).toBeUndefined();
    const result = output.structuredContent as ProviderUsageResult;
    assertWellFormedResult(result, seedData.workspaceId);
  });

  test("an office task agent (the coordinator surface) can also discover and call the tool", async ({
    testPage,
    apiClient,
    officeSeed,
  }) => {
    test.setTimeout(90_000);
    await installPackagedPlugin(testPage, apiClient);

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
    await installPackagedPlugin(testPage, apiClient);

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
    // ever sees the call — never a structured/successful result. Per MCP
    // convention, most tool-level rejections come back as a normal
    // CallToolResult with isError:true (not a transport-level error), so
    // check that rather than the transport `error` field.
    expect(output.structuredContent).toBeUndefined();
    expect(output.isError).toBe(true);
    expect((output.result ?? "").length).toBeGreaterThan(0);
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
    const listResText = await listRes.text();
    expect(listRes.status, listResText).toBe(200);
    const body = parseMcpJsonRpcBody(listResText) as {
      result?: { tools?: Array<{ name: string }> };
    };
    const toolNames = (body.result?.tools ?? []).map((t) => t.name);
    expect(toolNames).not.toContain(TOOL_NAME);
  });
});
