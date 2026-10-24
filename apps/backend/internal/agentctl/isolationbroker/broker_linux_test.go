//go:build linux

package isolationbroker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/server/process/isolation"
	"golang.org/x/sys/unix"
)

func TestBrokerRejectsExtraAndReorderedDescriptors(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	identities := make([]isolation.Identity, 0, 2)
	for _, path := range []string{first, second} {
		identity, err := isolation.Capture(path)
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, identity)
	}
	for _, extra := range []bool{false, true} {
		t.Run(map[bool]string{false: "reordered", true: "extra"}[extra], func(t *testing.T) {
			parent, child, err := SocketPair()
			if err != nil {
				t.Fatal(err)
			}
			epoch := make(chan struct{})
			server, err := NewServer(child, func() <-chan struct{} { return epoch })
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = server.Close() }()
			conn, err := net.FileConn(parent)
			_ = parent.Close()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			unixConn := conn.(*net.UnixConn)
			registration := Registration{TaskID: "task", SessionID: "session", InstanceID: "instance", Workspace: root, Identities: identities}
			body := []byte("body")
			payload, err := json.Marshal(frame{Registration: registration, Digest: sha256.Sum256(body)})
			if err != nil {
				t.Fatal(err)
			}
			one, err := isolation.Pin(identities[0])
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = one.Close() }()
			two, err := isolation.Pin(identities[1])
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = two.Close() }()
			fds := []int{int(two.File().Fd()), int(two.Parent().Fd()), int(one.File().Fd()), int(one.Parent().Fd())}
			if extra {
				fds = []int{int(one.File().Fd()), int(one.Parent().Fd()), int(two.File().Fd()), int(two.Parent().Fd()), int(one.File().Fd())}
			}
			if _, _, err := unixConn.WriteMsgUnix(payload, unix.UnixRights(fds...), nil); err != nil {
				t.Fatal(err)
			}
			var responseData [512]byte
			n, err := unixConn.Read(responseData[:])
			if err != nil {
				t.Fatal(err)
			}
			var reply response
			if err := json.Unmarshal(responseData[:n], &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Error == "" || reply.Receipt != "" {
				t.Fatalf("bad descriptors accepted: %+v", reply)
			}
		})
	}
}

func TestBrokerRejectsTruncatedDescriptorFrame(t *testing.T) {
	parent, child, err := SocketPair()
	if err != nil {
		t.Fatal(err)
	}
	epoch := make(chan struct{})
	server, err := NewServer(child, func() <-chan struct{} { return epoch })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	conn, err := net.FileConn(parent)
	_ = parent.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	root := t.TempDir()
	identity, err := isolation.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := isolation.Pin(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pin.Close() }()
	registration := Registration{TaskID: "task", SessionID: "session", InstanceID: "instance", Workspace: root, Identities: []isolation.Identity{identity}}
	payload, err := json.Marshal(frame{Registration: registration, Digest: sha256.Sum256([]byte("body"))})
	if err != nil {
		t.Fatal(err)
	}
	fds := make([]int, 2*maxGrants+1)
	for i := range fds {
		fds[i] = int(pin.File().Fd())
	}
	if _, _, err := conn.(*net.UnixConn).WriteMsgUnix(payload, unix.UnixRights(fds...), nil); err != nil {
		t.Fatal(err)
	}
	var responseData [512]byte
	n, err := conn.Read(responseData[:])
	if err != nil {
		t.Fatal(err)
	}
	var reply response
	if err := json.Unmarshal(responseData[:n], &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error == "" || reply.Receipt != "" {
		t.Fatalf("truncated descriptors accepted: %+v", reply)
	}
}

func TestBrokerTransfersPinnedObjectsOnce(t *testing.T) {
	parent, child, err := SocketPair()
	if err != nil {
		t.Fatal(err)
	}
	current := make(chan struct{})
	server, err := NewServer(child, func() <-chan struct{} { return current })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	client, err := NewClient(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	root := t.TempDir()
	missing := filepath.Join(root, "refs", "heads", "main.lock")
	identities := make([]isolation.Identity, 0, 2)
	for _, path := range []string{root, missing} {
		identity, err := isolation.Capture(path)
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, identity)
	}
	source, err := isolation.PinAll(identities)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	registration := Registration{TaskID: "task", SessionID: "session", InstanceID: "instance", Workspace: root, Identities: identities}
	body := []byte(`{"id":"instance","workspace_path":"` + root + `"}`)
	receipt, err := client.Register(context.Background(), body, registration, source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := server.Consume(receipt, body, registration, current)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Close() }()
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Consume(receipt, body, registration, current); err == nil {
		t.Fatal("receipt replay was accepted")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if pin, ok := got.Get(root); !ok || pin.File() == nil {
		t.Fatal("receiver lost independently owned descriptor")
	} else if flags, err := unix.FcntlInt(pin.File().Fd(), unix.F_GETFD, 0); err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("receiver descriptor is inheritable: flags=%d error=%v", flags, err)
	}
}

func TestBrokerRejectsBodyGenerationAndClosesPendingOnEOF(t *testing.T) {
	parent, child, err := SocketPair()
	if err != nil {
		t.Fatal(err)
	}
	epoch := make(chan struct{})
	server, err := NewServer(child, func() <-chan struct{} { return epoch })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	client, err := NewClient(parent)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	identity, err := isolation.Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := isolation.PinAll([]isolation.Identity{identity})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	reg := Registration{TaskID: "task", SessionID: "session", InstanceID: "instance", Workspace: root, Identities: []isolation.Identity{identity}}
	body := []byte("original body")
	receipt, err := client.Register(context.Background(), body, reg, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Consume(receipt, []byte("changed body"), reg, epoch); err == nil {
		t.Fatal("changed HTTP body consumed registration")
	}
	receipt, err = client.Register(context.Background(), body, reg, source)
	if err != nil {
		t.Fatal(err)
	}
	stale := epoch
	close(epoch)
	epoch = make(chan struct{})
	if _, err := server.Consume(receipt, body, reg, stale); err == nil {
		t.Fatal("stale control generation consumed registration")
	}
	receipt, err = client.Register(context.Background(), body, reg, source)
	if err != nil {
		t.Fatal(err)
	}
	if receipt == "" {
		t.Fatal("missing fresh receipt")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-server.done:
	case <-time.After(time.Second):
		t.Fatal("broker did not close pending descriptors on EOF")
	}
	server.mu.Lock()
	remaining := len(server.entries)
	server.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("broker retained %d pending registrations after EOF", remaining)
	}
}
