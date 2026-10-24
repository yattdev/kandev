//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/kandev/kandev/internal/agentctl/isolationbroker"
)

func inheritedIsolationBroker(current func() <-chan struct{}) (*isolationbroker.Server, error) {
	value := os.Getenv("KANDEV_ISOLATION_BROKER_FD")
	if value == "" {
		return nil, nil
	}
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return nil, fmt.Errorf("invalid inherited isolation broker descriptor")
	}
	return isolationbroker.NewServer(os.NewFile(uintptr(fd), "inherited-isolation-broker"), current)
}
