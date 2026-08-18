import { describe, expect, it } from "vitest";
import { buildSaveConfig, type ExecutorProfileConfigForm } from "./serialize-executor-config";

function form(overrides: Partial<ExecutorProfileConfigForm> = {}): ExecutorProfileConfigForm {
  return {
    isSprites: false,
    networkPolicyRules: [],
    isRemote: true,
    remoteCredentials: [],
    configBundleIds: [],
    agentEnvVars: {},
    gitIdentityMode: "override",
    localGitIdentity: { userName: "", userEmail: "" },
    gitUserName: "",
    gitUserEmail: "",
    isDocker: false,
    dockerfile: "",
    imageTag: "",
    allowUserNamespaces: false,
    isSSH: false,
    sshShell: "",
    ...overrides,
  };
}

describe("buildSaveConfig", () => {
  it("persists selected configuration without requiring authentication", () => {
    const config = buildSaveConfig(form({ configBundleIds: ["mock.settings"] }), {
      remote_credentials: "stale",
      keep: "yes",
    });

    expect(config).toEqual({
      agent_config_bundles: '["mock.settings"]',
      keep: "yes",
    });
  });

  it("persists authentication without requiring configuration", () => {
    const config = buildSaveConfig(form({ remoteCredentials: ["codex-auth"] }));

    expect(config).toEqual({ remote_credentials: '["codex-auth"]' });
  });

  it("persists allowUserNamespaces when enabled on a Docker profile", () => {
    const config = buildSaveConfig(
      form({ isDocker: true, allowUserNamespaces: true }),
    );
    expect(config.allow_user_namespaces).toBe("true");
  });

  it("removes allowUserNamespaces key when disabled", () => {
    const config = buildSaveConfig(
      form({ isDocker: true, allowUserNamespaces: false }),
    );
    expect(config.allow_user_namespaces).toBeUndefined();
  });

  it("removes allowUserNamespaces when not a Docker profile", () => {
    const config = buildSaveConfig(
      form({ isDocker: false, allowUserNamespaces: true }),
    );
    expect(config.allow_user_namespaces).toBeUndefined();
  });
});
