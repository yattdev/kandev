package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/agent/agents"
	"github.com/kandev/kandev/internal/agent/docker"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/worktree"
)

func TestGitMetadataMountsAllowOnlyOwnedLinkedWorktreeMetadata(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	runContainerGit(t, "", "init", "-b", "main", repo)
	runContainerGit(t, repo, "config", "user.email", "test@example.com")
	runContainerGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runContainerGit(t, repo, "add", "file")
	runContainerGit(t, repo, "commit", "-m", "initial")
	runContainerGit(t, repo, "remote", "add", "origin", "https://github.com/example/project.git")
	checkout := filepath.Join(t.TempDir(), "checkout")
	runContainerGit(t, repo, "worktree", "add", "-b", "task", checkout)

	projection, err := worktree.ResolveGitMetadataForRepository(checkout, repo)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := newCMTest(t).buildContainerConfig(ContainerConfig{
		AgentConfig:            newConfigStubAgent(),
		WorkspacePath:          checkout,
		InstanceID:             "0123456789abcdef",
		TaskID:                 "task-1",
		GitMetadataProjections: []*worktree.GitMetadataProjection{projection},
	})
	if err != nil {
		t.Fatal(err)
	}
	mounts := configured.Mounts
	assertGitMount(t, mounts, projection.CommonDir, true)
	assertGitMount(t, mounts, projection.GitDir, false)
	for _, mount := range mounts {
		if mount.ReadOnly {
			continue
		}
		if _, err := os.Stat(mount.Source); err != nil {
			t.Errorf("writable Git mount source %q must exist before Docker creates the container: %v", mount.Source, err)
		}
	}
	for _, mount := range mounts {
		if mount.ReadOnly {
			continue
		}
		for _, path := range []string{filepath.Dir(projection.CurrentRefPath), filepath.Dir(projection.ReflogPath)} {
			if mount.Target == path {
				t.Errorf("shared Git directory %q must not be mounted writable", path)
			}
		}
	}
	privateGitDir := filepath.Join(projection.GitDir, "kandev-agent-git")
	if !hasGitMount(mounts, privateGitDir, projection.GitDir, false) {
		t.Fatalf("task-private Git metadata must be mounted over the linked-worktree admin dir: %#v", mounts)
	}
	remoteURL := strings.TrimSpace(string(runContainerGitOutput(t, "--git-dir", privateGitDir, "remote", "get-url", "origin")))
	if remoteURL != "https://github.com/example/project.git" {
		t.Fatalf("task-private origin = %q, want source repository URL", remoteURL)
	}
	sharedSiblingRef := filepath.Join(projection.CommonDir, "refs", "heads", "main")
	siblingBefore, err := os.ReadFile(sharedSiblingRef)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(privateGitDir, "refs", "heads", "task.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "change"), []byte("task change"), 0o600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) ([]byte, error) {
		command := exec.Command("git", append([]string{"--git-dir", privateGitDir, "--work-tree", checkout}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Task Agent", "GIT_AUTHOR_EMAIL=task@example.com", "GIT_COMMITTER_NAME=Task Agent", "GIT_COMMITTER_EMAIL=task@example.com")
		return command.CombinedOutput()
	}
	if output, err := git("add", "change"); err != nil {
		t.Fatalf("git add through task metadata: %v: %s", err, output)
	}
	if output, err := git("commit", "-m", "task commit"); err == nil {
		t.Fatalf("commit must respect an occupied native ref lock: %s", output)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if output, err := git("commit", "-m", "task commit"); err != nil {
		t.Fatalf("git commit through task metadata: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(privateGitDir, "logs", "HEAD")); err != nil {
		t.Fatalf("task reflog was not updated: %v", err)
	}
	siblingAfter, err := os.ReadFile(sharedSiblingRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(siblingAfter) != string(siblingBefore) {
		t.Fatalf("task commit changed sibling ref from %q to %q", siblingBefore, siblingAfter)
	}
	if output, err := git("update-ref", "refs/heads/main", "HEAD~0"); err != nil {
		t.Fatalf("mutate the task-private copy of a sibling ref: %v: %s", err, output)
	}
	siblingAfter, err = os.ReadFile(sharedSiblingRef)
	if err != nil {
		t.Fatal(err)
	}
	if string(siblingAfter) != string(siblingBefore) {
		t.Fatalf("task-private sibling ref update changed shared ref from %q to %q", siblingBefore, siblingAfter)
	}
	if _, err := os.Stat(filepath.Join(privateGitDir, "refs", "heads", "task.lock")); !os.IsNotExist(err) {
		t.Fatalf("native ref lock was not released: %v", err)
	}
	if _, err := os.Stat(filepath.Join(privateGitDir, "logs", "refs", "heads", "task.lock")); !os.IsNotExist(err) {
		t.Fatalf("native reflog lock was not released: %v", err)
	}
}

func runContainerGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func runContainerGitOutput(t *testing.T, args ...string) []byte {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return output
}

func assertGitMount(t *testing.T, mounts []docker.MountConfig, target string, readOnly bool) {
	t.Helper()
	for _, mount := range mounts {
		if mount.Target == target && mount.ReadOnly == readOnly {
			return
		}
	}
	t.Fatalf("missing mount target=%q readOnly=%t: %#v", target, readOnly, mounts)
}

func hasGitMount(mounts []docker.MountConfig, source, target string, readOnly bool) bool {
	for _, mount := range mounts {
		if mount.Source == source && mount.Target == target && mount.ReadOnly == readOnly {
			return true
		}
	}
	return false
}

// configStubAgent wraps MockAgent and overrides Runtime() with a fixed
// RuntimeConfig that mimics ACP agents (image+tag, {workspace} placeholder).
type configStubAgent struct {
	*agents.MockAgent
	rt *agents.RuntimeConfig
}

func (a *configStubAgent) Runtime() *agents.RuntimeConfig { return a.rt }

func newCMTest(t *testing.T) *ContainerManager {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return &ContainerManager{
		logger:         log,
		networkName:    "kandev",
		commandBuilder: NewCommandBuilder(),
	}
}

func newConfigStubAgent() *configStubAgent {
	return &configStubAgent{
		MockAgent: agents.NewMockAgent(),
		rt: &agents.RuntimeConfig{
			Image:      "kandev/multi-agent",
			Tag:        "latest",
			Cmd:        agents.Cmd("/bin/true").Build(),
			WorkingDir: "{workspace}",
			Mounts:     []agents.MountTemplate{{Source: "{workspace}", Target: "/workspace"}},
			ResourceLimits: agents.ResourceLimits{
				MemoryMB: 256,
				CPUCores: 0.5,
			},
		},
	}
}

func TestBuildContainerConfig_ExpandsWorkingDirPlaceholder(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig: newConfigStubAgent(),
		// WorkspacePath empty → clone-inside-container path; should default to /workspace.
		InstanceID: "0123456789abcdef",
		TaskID:     "task-1",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if got.WorkingDir != "/workspace" {
		t.Errorf("WorkingDir = %q, want /workspace (placeholder must be expanded)", got.WorkingDir)
	}
	if strings.Contains(got.WorkingDir, "{") {
		t.Errorf("WorkingDir still contains placeholder syntax: %q", got.WorkingDir)
	}
}

func TestBuildContainerConfig_WorkingDirIsAlwaysContainerPath(t *testing.T) {
	// Regression: WorkingDir is the container-side path, not the host path.
	// In host bind-mount mode, WorkspacePath holds the host path; the bind
	// mount target is the in-container /workspace, so WorkingDir must point
	// at the container target — otherwise Docker happily starts the
	// container in /host/path/to/repo (which doesn't exist inside) and the
	// agent runs in an unrelated directory.
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig:   newConfigStubAgent(),
		WorkspacePath: "/host/path/to/repo",
		InstanceID:    "0123456789abcdef",
		TaskID:        "task-1",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if got.WorkingDir != "/workspace" {
		t.Errorf("WorkingDir = %q, want /workspace (container-side path)", got.WorkingDir)
	}
}

func TestBuildContainerConfigPreflightsBrokerBeforePrepareClone(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig: newConfigStubAgent(),
		InstanceID:  "0123456789abcdef",
		TaskID:      "task-1",
		Credentials: map[string]string{
			envKeyGitHubCredentialBrokerURL: "https://kandev.example/api/v1/github/credentials/resolve",
			envKeyGitHubCredentialLease:     "lease",
		},
		PrepareScript: "git clone https://github.com/acme/widgets.git /workspace",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if len(got.Entrypoint) != 3 {
		t.Fatalf("entrypoint = %#v", got.Entrypoint)
	}
	script := got.Entrypoint[2]
	probeAt := strings.Index(script, "curl -sS --connect-timeout")
	cloneAt := strings.Index(script, `eval "$KANDEV_PREPARE_SCRIPT"`)
	if probeAt < 0 || cloneAt < 0 || probeAt >= cloneAt {
		t.Fatalf("broker probe must precede prepare/clone: %s", script)
	}
	if !strings.Contains(script, `exit "$probe_rc"`) {
		t.Fatalf("unreachable broker must stop bootstrap before clone: %s", script)
	}
}

func TestBuildContainerConfigBoundsPrepareScriptBeforeAgentctl(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig:   newConfigStubAgent(),
		InstanceID:    "0123456789abcdef",
		TaskID:        "task-1",
		PrepareScript: "sleep 1",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if len(got.Entrypoint) != 3 {
		t.Fatalf("entrypoint = %#v", got.Entrypoint)
	}

	script := got.Entrypoint[2]
	want := fmt.Sprintf(
		"timeout -s TERM -k 1s %s sh -c",
		"600s",
	)
	if !strings.Contains(script, want) {
		t.Fatalf("prepare timeout = %q, want bootstrap to contain %q", script, want)
	}
	if strings.Index(script, want) >= strings.Index(script, "exec /usr/local/bin/agentctl") {
		t.Fatalf("prepare timeout must run before agentctl: %s", script)
	}
}

func TestBuildContainerConfigPublishesManagedGitCredentialHelperBeforeAgentctlStartup(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig: newConfigStubAgent(),
		InstanceID:  "0123456789abcdef",
		TaskID:      "task-1",
		Credentials: map[string]string{
			envKeyGitHubCredentialBrokerURL: "https://kandev.example/api/v1/github/credentials/resolve",
			envKeyGitHubCredentialLease:     "lease",
		},
		PrepareScript: "git clone https://github.com/acme/widgets.git /workspace",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	want := "KANDEV_GITHUB_CREDENTIAL_HELPER_PATH=/usr/local/bin/agentctl"
	if !containsExactString(got.Env, want) {
		t.Fatalf("container env missing pre-start credential helper %q: %#v", want, got.Env)
	}
}

func TestBuildContainerConfigScrubsForkPRCredentialsBeforeAgentctl(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig:   newConfigStubAgent(),
		InstanceID:    "0123456789abcdef",
		TaskID:        "task-1",
		Credentials:   map[string]string{"GITHUB_TOKEN": "secret"},
		Metadata:      map[string]interface{}{metadataCheckoutRef: "refs/pull/3527/head"},
		PrepareScript: "echo prepare",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	script := got.Entrypoint[2]
	if !strings.Contains(script, "${"+selectedCheckoutMarker+":-}") {
		t.Fatalf("bootstrap marker check missing: %s", script)
	}
	if !strings.Contains(script, selectedCheckoutCredentialScrubCommands) {
		t.Fatalf("bootstrap credential scrub missing: %s", script)
	}
	if !strings.Contains(script, "rm -f /run/kandev/auth.env") {
		t.Fatalf("bootstrap must remove auth material: %s", script)
	}
}

func containsExactString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestBuildContainerConfig_ImageDefaultsToRuntime(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig: newConfigStubAgent(),
		InstanceID:  "0123456789abcdef",
		TaskID:      "task-1",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if got.Image != "kandev/multi-agent:latest" {
		t.Errorf("Image = %q, want kandev/multi-agent:latest", got.Image)
	}
}

// TestBuildContainerConfig_SecurityOptNilByDefault verifies AC-1: a profile
// with AllowUserNamespaces off (the default) produces SecurityOpt == nil,
// byte-identical to today's launch.
func TestBuildContainerConfig_SecurityOptNilByDefault(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig: newConfigStubAgent(),
		InstanceID:  "0123456789abcdef",
		TaskID:      "task-1",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if got.SecurityOpt != nil {
		t.Errorf("SecurityOpt = %v, want nil when AllowUserNamespaces is off", got.SecurityOpt)
	}
}

// TestBuildContainerConfig_SecurityOptForUserNamespaces verifies AC-2: a
// profile with AllowUserNamespaces on produces seccomp and apparmor opts.
func TestBuildContainerConfig_SecurityOptForUserNamespaces(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig:         newConfigStubAgent(),
		InstanceID:          "0123456789abcdef",
		TaskID:              "task-1",
		AllowUserNamespaces: true,
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if len(got.SecurityOpt) != 2 {
		t.Fatalf("SecurityOpt = %v, want exactly 2 entries", got.SecurityOpt)
	}
	assertHasSecurityOpt(t, got.SecurityOpt, "apparmor=unconfined")
	if !strings.HasPrefix(got.SecurityOpt[0], "seccomp=") {
		t.Errorf("SecurityOpt[0] = %q, want seccomp=<json> prefix", got.SecurityOpt[0])
	}
}

func TestBuildContainerConfig_ImageTagOverrideWins(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig:      newConfigStubAgent(),
		InstanceID:       "0123456789abcdef",
		TaskID:           "task-1",
		ImageTagOverride: "kandev/agent:custom",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if got.Image != "kandev/agent:custom" {
		t.Errorf("Image = %q, want kandev/agent:custom (profile override must win over rt.Image)", got.Image)
	}
}

func TestBuildContainerConfig_LabelsExecutorProfileAndTaskEnvironment(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig:       newConfigStubAgent(),
		InstanceID:        "0123456789abcdef",
		TaskID:            "task-1",
		TaskTitle:         "Readable Task Title",
		SessionID:         "session-1",
		TaskEnvironmentID: "env-1",
		ExecutorProfileID: "profile-1",
		ImageTagOverride:  "kandev/agent:custom",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	assertLabel(t, got.Labels, "kandev.managed", boolStringTrue)
	assertLabel(t, got.Labels, "kandev.task_id", "task-1")
	assertLabel(t, got.Labels, "kandev.task_title", "Readable Task Title")
	assertLabel(t, got.Labels, "kandev.session_id", "session-1")
	assertLabel(t, got.Labels, "kandev.task_environment_id", "env-1")
	assertLabel(t, got.Labels, "kandev.executor_profile_id", "profile-1")
	assertLabel(t, got.Labels, "kandev.profile_id", "profile-1")
	assertLabel(t, got.Labels, "com.kandev.image", "kandev/agent:custom")
}

func TestBuildContainerConfig_LabelsE2EDockerScope(t *testing.T) {
	t.Setenv("KANDEV_E2E_DOCKER_SCOPE", "e2e-test-scope")
	cm := newCMTest(t)
	got, err := cm.buildContainerConfig(ContainerConfig{
		AgentConfig: newConfigStubAgent(),
		InstanceID:  "0123456789abcdef",
		TaskID:      "task-1",
	})
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	assertLabel(t, got.Labels, "kandev.e2e.run", "e2e-test-scope")
}

func TestBuildContainerConfig_PublishesAgentctlPorts(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig: newConfigStubAgent(),
		InstanceID:  "0123456789abcdef",
		TaskID:      "task-1",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	if len(got.PortBindings) == 0 {
		t.Fatal("expected agentctl ports to be published")
	}
	assertHasPortBinding(t, got.PortBindings, AgentCtlPort)
	assertHasPortBinding(t, got.PortBindings, dockerAgentctlInstancePortBase)
	assertHasPortBinding(t, got.PortBindings, dockerAgentctlInstancePortMax)
	assertEnvContains(t, got.Env, "AGENTCTL_INSTANCE_PORT_BASE=41001")
	assertEnvContains(t, got.Env, "AGENTCTL_INSTANCE_PORT_MAX=41100")
}

// TestDockerAgentctlPortBindings is a direct test for the helper that
// generates the published-port set for every kandev-managed Docker agent
// container. A regression here (wrong port range, missing agentctl port,
// non-loopback host IP) would silently break container reconnect, since
// `resolveDockerEndpoint` falls back to the container IP when the published
// port lookup fails.
func TestDockerAgentctlPortBindings(t *testing.T) {
	bindings := dockerAgentctlPortBindings()

	wantTotal := 1 + (dockerAgentctlInstancePortMax - dockerAgentctlInstancePortBase + 1)
	if len(bindings) != wantTotal {
		t.Fatalf("got %d bindings, want %d (control + instance range)", len(bindings), wantTotal)
	}

	// Control port must be present.
	assertHasPortBinding(t, bindings, AgentCtlPort)

	// Every port in the instance range must be present and bound to loopback
	// with a kernel-assigned host port.
	have := make(map[int]docker.PortBindingConfig, len(bindings))
	for _, b := range bindings {
		have[b.ContainerPort] = b
	}
	for port := dockerAgentctlInstancePortBase; port <= dockerAgentctlInstancePortMax; port++ {
		b, ok := have[port]
		if !ok {
			t.Fatalf("missing instance port %d in published bindings", port)
		}
		if b.HostIP != "127.0.0.1" {
			t.Errorf("port %d host_ip = %q, want 127.0.0.1", port, b.HostIP)
		}
		if b.HostPort != "0" {
			t.Errorf("port %d host_port = %q, want kernel-assigned (\"0\")", port, b.HostPort)
		}
	}
}

// TestBuildContainerConfig_SessionDirIsKandevManagedForEveryAgent locks in
// the agent-agnostic guarantee that bind sources for SessionDirTemplate
// resolve to <kandev-home>/agent-sessions/<instance>/<dotdir> and never to
// the user's host home — the codex bug was a leak of host state into the
// container, and any agent with a SessionDirTemplate is at the same risk.
func TestBuildContainerConfig_SessionDirIsKandevManagedForEveryAgent(t *testing.T) {
	allAgents := []struct {
		name string
		ag   agents.Agent
	}{
		{"codex-acp", agents.NewCodexACP()},
		{"claude-acp", agents.NewClaudeACP()},
		{"opencode-acp", agents.NewOpenCodeACP()},
		{"devin-acp", agents.NewDevinACP()},
		{"copilot-acp", agents.NewCopilotACP()},
		{"amp-acp", agents.NewAmpACP()},
		{"gemini", agents.NewGemini()},
		{"auggie", agents.NewAuggie()},
		{"grok-acp", agents.NewGrokACP()},
		{"muse-acp", agents.NewMuseACP()},
	}
	const kandevHome = "/tmp/kandev-test-home"
	const instanceID = "0123456789abcdef"
	expectedRoot := filepath.Join(kandevHome, "agent-sessions", instanceID)

	for _, tc := range allAgents {
		t.Run(tc.name, func(t *testing.T) {
			rt := tc.ag.Runtime()
			if rt == nil {
				t.Skipf("%s has no Runtime", tc.name)
			}
			// expandMounts only adds the session-dir bind when BOTH fields
			// are set; agents that omit one rely on the in-container
			// SetupScript for auth and never bind-mount the host home in the
			// first place. Skip those — the test guards the resolution shape
			// only for agents that DO add the bind mount.
			if rt.SessionConfig.SessionDirTemplate == "" || rt.SessionConfig.SessionDirTarget == "" {
				t.Skipf("%s has no full SessionDirTemplate+SessionDirTarget pair (no bind mount today)", tc.name)
			}

			cm := newCMTest(t)
			cm.kandevHomeDir = kandevHome
			cfg := ContainerConfig{
				AgentConfig: tc.ag,
				InstanceID:  instanceID,
				TaskID:      "task-1",
			}

			got, err := cm.buildContainerConfig(cfg)
			if err != nil {
				t.Fatalf("buildContainerConfig: %v", err)
			}

			target := rt.SessionConfig.SessionDirTarget
			var found *docker.MountConfig
			for i := range got.Mounts {
				if got.Mounts[i].Target == target {
					found = &got.Mounts[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("expected mount for SessionDirTarget %q, got %+v", target, got.Mounts)
			}
			if !strings.HasPrefix(found.Source, expectedRoot) {
				t.Fatalf("session-dir mount source %q not under %q (host home leaked into container?)",
					found.Source, expectedRoot)
			}
			if strings.Contains(found.Source, "{home}") {
				t.Fatalf("session-dir mount source %q still references {home} placeholder", found.Source)
			}
		})
	}
}

func TestBuildContainerConfig_MuseMountContainsSeededAuthAndSessionData(t *testing.T) {
	cm := newCMTest(t)
	cm.kandevHomeDir = "/tmp/kandev-test-home"
	const instanceID = "muse-instance"
	ag := agents.NewMuseACP()

	got, err := cm.buildContainerConfig(ContainerConfig{
		AgentConfig: ag,
		InstanceID:  instanceID,
		TaskID:      "task-1",
	})
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	root := filepath.Join(cm.kandevHomeDir, "agent-sessions", instanceID)
	var mount *docker.MountConfig
	for i := range got.Mounts {
		if got.Mounts[i].Target == "/root" {
			mount = &got.Mounts[i]
			break
		}
	}
	if mount == nil {
		t.Fatalf("expected Muse executor-home mount at /root, got %+v", got.Mounts)
	}
	if mount.Source != root {
		t.Fatalf("Muse mount source = %q, want isolated executor root %q", mount.Source, root)
	}

	auth := ag.RemoteAuth()
	if auth == nil || len(auth.Methods) == 0 {
		t.Fatal("Muse must declare remote auth files")
	}
	seededAuth := filepath.Join(root, auth.Methods[0].TargetRelDir, "auth.json")
	if !strings.HasPrefix(seededAuth, mount.Source+string(filepath.Separator)) {
		t.Fatalf("seeded auth path %q is outside Muse mount %q", seededAuth, mount.Source)
	}
	if !strings.HasPrefix(filepath.Join(root, ".local", "share", "muse"), mount.Source+string(filepath.Separator)) {
		t.Fatalf("Muse session path is outside Muse mount %q", mount.Source)
	}
}

func TestBuildContainerConfig_MountsDevinCredentialSessionDir(t *testing.T) {
	cm := newCMTest(t)
	cm.kandevHomeDir = "/tmp/kandev-test-home"
	instanceID := "devin-instance"

	got, err := cm.buildContainerConfig(ContainerConfig{
		AgentConfig: agents.NewDevinACP(),
		InstanceID:  instanceID,
		TaskID:      "task-1",
	})
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	wantSource := filepath.Join(cm.kandevHomeDir, "agent-sessions", instanceID, ".local/share/devin")
	wantTarget := "/root/.local/share/devin"
	for _, mount := range got.Mounts {
		if mount.Source == wantSource && mount.Target == wantTarget {
			return
		}
	}
	t.Fatalf("expected Devin credential mount %s -> %s, got %+v", wantSource, wantTarget, got.Mounts)
}

func TestBuildContainerConfig_MountsLocalClonePath(t *testing.T) {
	cm := newCMTest(t)
	cfg := ContainerConfig{
		AgentConfig:    newConfigStubAgent(),
		InstanceID:     "0123456789abcdef",
		TaskID:         "task-1",
		LocalClonePath: "/tmp/e2e-docker-remote.git",
	}

	got, err := cm.buildContainerConfig(cfg)
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	assertHasMount(t, got.Mounts, "/tmp/e2e-docker-remote.git", "/tmp/e2e-docker-remote.git", true)
}

func assertLabel(t *testing.T, labels map[string]string, key, want string) {
	t.Helper()
	if labels[key] != want {
		t.Fatalf("label %s = %q, want %q in %#v", key, labels[key], want, labels)
	}
}

func assertHasMount(t *testing.T, mounts []docker.MountConfig, source, target string, readOnly bool) {
	t.Helper()
	for _, mount := range mounts {
		if mount.Source == source && mount.Target == target && mount.ReadOnly == readOnly {
			return
		}
	}
	t.Fatalf("missing mount source=%q target=%q readOnly=%v in %#v", source, target, readOnly, mounts)
}

func assertHasSecurityOpt(t *testing.T, opts []string, want string) {
	t.Helper()
	for _, opt := range opts {
		if opt == want {
			return
		}
	}
	t.Fatalf("missing SecurityOpt %q in %#v", want, opts)
}

func assertHasPortBinding(t *testing.T, bindings []docker.PortBindingConfig, port int) {
	t.Helper()
	for _, binding := range bindings {
		if binding.ContainerPort == port && binding.HostIP == "127.0.0.1" && binding.HostPort == "0" {
			return
		}
	}
	t.Fatalf("missing published port binding for %d/tcp: %#v", port, bindings)
}

func assertEnvContains(t *testing.T, env []string, want string) {
	t.Helper()
	for _, item := range env {
		if item == want {
			return
		}
	}
	t.Fatalf("missing env %q in %#v", want, env)
}
