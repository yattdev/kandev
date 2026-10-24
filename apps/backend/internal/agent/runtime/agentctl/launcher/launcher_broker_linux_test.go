//go:build linux

package launcher

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSetupIsolationBrokerUsesInheritedPrivateEndpoint(t *testing.T) {
	cmd := exec.Command("true")
	cmd.Env = append(os.Environ(), "KANDEV_ISOLATION_BROKER_FD=99")
	client, child, err := setupIsolationBroker(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	defer func() { _ = child.Close() }()
	if len(cmd.ExtraFiles) != 1 || cmd.ExtraFiles[0] != child {
		t.Fatal("broker endpoint was not inherited only by the child")
	}
	if strings.Count(strings.Join(cmd.Env, "\n"), "KANDEV_ISOLATION_BROKER_FD=") != 1 ||
		!strings.Contains(strings.Join(cmd.Env, "\n"), "KANDEV_ISOLATION_BROKER_FD=3") {
		t.Fatal("child did not receive the private endpoint number")
	}
}
