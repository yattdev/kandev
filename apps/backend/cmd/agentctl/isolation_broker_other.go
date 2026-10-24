//go:build !linux

package main

import "github.com/kandev/kandev/internal/agentctl/isolationbroker"

func inheritedIsolationBroker(_ func() <-chan struct{}) (*isolationbroker.Server, error) {
	return nil, nil
}
