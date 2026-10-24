//go:build !linux

package launcher

import (
	"os"
	"os/exec"

	"github.com/kandev/kandev/internal/agentctl/isolationbroker"
)

func setupIsolationBroker(_ *exec.Cmd) (*isolationbroker.Client, *os.File, error) {
	return nil, nil, nil
}
