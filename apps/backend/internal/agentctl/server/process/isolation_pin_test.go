//go:build linux

package process

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/process/isolation"
	"github.com/kandev/kandev/internal/common/logger"
	"golang.org/x/sys/unix"
)

func TestManagerTeardownClosesOwnedIsolationPins(t *testing.T) {
	root := t.TempDir()
	identity, err := isolation.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	pins, err := isolation.PinAll([]isolation.Identity{identity})
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := pins.Get(root)
	fd := int(pin.File().Fd())
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(&config.InstanceConfig{WorkDir: root}, log)
	m.SetIsolationPins(pins)
	if err := m.StopForTeardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(fd, &unix.Stat_t{}); err != unix.EBADF {
		t.Fatalf("teardown left isolation descriptor open: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("teardown damaged source object: %v", err)
	}
}

func TestTimedOutTeardownEventuallyClosesPinsAfterAdmissionDrains(t *testing.T) {
	root := t.TempDir()
	identity, err := isolation.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	pins, err := isolation.PinAll([]isolation.Identity{identity})
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := pins.Get(root)
	fd := int(pin.File().Fd())
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(&config.InstanceConfig{WorkDir: root}, log)
	m.SetIsolationPins(pins)
	release, err := m.admitStart()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.StopForTeardown(ctx); err == nil {
		t.Fatal("teardown unexpectedly drained held admission")
	}
	release()
	deadline := time.After(3 * time.Second)
	for {
		if err := unix.Fstat(fd, &unix.Stat_t{}); err == unix.EBADF {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed-out teardown leaked pin descriptor after admission drained")
		case <-time.After(time.Millisecond):
		}
	}
}
