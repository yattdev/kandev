//go:build linux

package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kandev/kandev/internal/agentctl/isolationbroker"
)

func setupIsolationBroker(cmd *exec.Cmd) (*isolationbroker.Client, *os.File, error) {
	filtered := cmd.Env[:0]
	for _, entry := range cmd.Env {
		if !strings.HasPrefix(entry, "KANDEV_ISOLATION_BROKER_FD=") {
			filtered = append(filtered, entry)
		}
	}
	cmd.Env = filtered
	parent, child, err := isolationbroker.SocketPair()
	if err != nil {
		return nil, nil, err
	}
	client, err := isolationbroker.NewClient(parent)
	if err != nil {
		_ = child.Close()
		return nil, nil, err
	}
	childFD := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, child)
	cmd.Env = append(cmd.Env, fmt.Sprintf("KANDEV_ISOLATION_BROKER_FD=%d", childFD))
	return client, child, nil
}
