//go:build linux

package isolationbroker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/kandev/kandev/internal/agentctl/server/process/isolation"
	"golang.org/x/sys/unix"
)

const (
	maxGrants       = 64
	maxFrame        = 65536
	maxPending      = 32
	pendingLifetime = 15 * time.Second
)

type Registration struct {
	TaskID     string               `json:"task_id"`
	SessionID  string               `json:"session_id"`
	InstanceID string               `json:"instance_id"`
	Workspace  string               `json:"workspace"`
	Identities []isolation.Identity `json:"identities"`
}

type frame struct {
	Registration
	Digest [sha256.Size]byte `json:"digest"`
}

type response struct {
	Receipt string `json:"receipt,omitempty"`
	Error   string `json:"error,omitempty"`
}

func SocketPair() (*os.File, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(fds[0]), "isolation-broker-parent"), os.NewFile(uintptr(fds[1]), "isolation-broker-child"), nil
}

func unixConn(file *os.File) (*net.UnixConn, error) {
	conn, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("isolation broker is not a Unix socket")
	}
	return unixConn, nil
}

type Client struct {
	mu   sync.Mutex
	conn *net.UnixConn
}

func NewClient(file *os.File) (*Client, error) {
	conn, err := unixConn(file)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) Register(ctx context.Context, body []byte, reg Registration, source *isolation.Set) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(reg.Identities) == 0 || len(reg.Identities) > maxGrants || len(body) == 0 || source == nil {
		return "", errors.New("invalid isolation registration")
	}
	if err := source.Validate(); err != nil {
		return "", err
	}
	message, err := json.Marshal(frame{Registration: reg, Digest: sha256.Sum256(body)})
	if err != nil || len(message) > maxFrame {
		return "", errors.New("isolation registration frame exceeds limit")
	}
	fds, err := registrationFiles(reg.Identities, source)
	if err != nil {
		return "", err
	}
	return c.exchangeWithContext(ctx, message, fds)
}

func (c *Client) exchangeWithContext(ctx context.Context, message []byte, fds []int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	deadline := time.Now().Add(5 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = c.conn.SetDeadline(time.Now())
		case <-stop:
		}
	}()
	receipt, err := c.exchange(message, fds)
	close(stop)
	<-done
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		// A missing ACK leaves the next reply ambiguous; EOF also revokes pending FDs.
		_ = c.conn.Close()
		return "", err
	}
	return receipt, nil
}

func (c *Client) exchange(message []byte, fds []int) (string, error) {
	n, oobn, err := c.conn.WriteMsgUnix(message, unix.UnixRights(fds...), nil)
	if err != nil {
		return "", err
	}
	if n != len(message) || oobn != len(unix.UnixRights(fds...)) {
		return "", errors.New("incomplete isolation registration transfer")
	}
	var buf [512]byte
	n, err = c.conn.Read(buf[:])
	if err != nil {
		return "", err
	}
	var reply response
	if err := json.Unmarshal(buf[:n], &reply); err != nil {
		return "", err
	}
	if reply.Error != "" || reply.Receipt == "" {
		return "", fmt.Errorf("isolation registration rejected: %s", reply.Error)
	}
	return reply.Receipt, nil
}

func registrationFiles(identities []isolation.Identity, source *isolation.Set) ([]int, error) {
	fds := make([]int, 0, 2*len(identities))
	for _, identity := range identities {
		pin, ok := source.Get(identity.Path)
		if !ok || pin.File() == nil {
			return nil, errors.New("isolation source descriptor missing")
		}
		fds = append(fds, int(pin.File().Fd()))
		if identity.Exists {
			if pin.Parent() == nil {
				return nil, errors.New("isolation parent descriptor missing")
			}
			fds = append(fds, int(pin.Parent().Fd()))
		}
	}
	return fds, nil
}

type pending struct {
	frame frame
	pins  *isolation.Set
	epoch <-chan struct{}
	timer *time.Timer
}

type Created struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
}

type completed struct {
	created Created
	epoch   <-chan struct{}
	timer   *time.Timer
}

type Server struct {
	conn      *net.UnixConn
	current   func() <-chan struct{}
	origin    <-chan struct{}
	mu        sync.Mutex
	entries   map[string]*pending
	claimed   map[string]*time.Timer
	completed map[string]*completed
	closed    bool
	done      chan struct{}
}

func NewServer(file *os.File, current func() <-chan struct{}) (*Server, error) {
	conn, err := unixConn(file)
	if err != nil {
		return nil, err
	}
	s := &Server{conn: conn, current: current, origin: current(), entries: make(map[string]*pending), claimed: make(map[string]*time.Timer), completed: make(map[string]*completed), done: make(chan struct{})}
	go s.run()
	return s, nil
}

func (s *Server) run() {
	defer close(s.done)
	defer s.Revoke()
	for {
		var payload [maxFrame]byte
		ancillary := make([]byte, unix.CmsgSpace(2*maxGrants*4))
		n, oobn, flags, _, err := s.conn.ReadMsgUnix(payload[:], ancillary)
		if err != nil {
			return
		}
		files, parseErr := receivedFiles(ancillary[:oobn])
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			parseErr = errors.New("truncated isolation registration")
		}
		reply := s.accept(payload[:n], files, parseErr)
		encoded, _ := json.Marshal(reply)
		if _, err := s.conn.Write(encoded); err != nil {
			return
		}
	}
}

func receivedFiles(oob []byte) ([]*os.File, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	files := make([]*os.File, 0)
	for _, message := range messages {
		fds, err := unix.ParseUnixRights(&message)
		if err != nil {
			closeFiles(files)
			return nil, err
		}
		for _, fd := range fds {
			unix.CloseOnExec(fd)
			files = append(files, os.NewFile(uintptr(fd), "isolation-received"))
		}
	}
	return files, nil
}

func closeFiles(files []*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}

func (s *Server) accept(payload []byte, files []*os.File, parseErr error) response {
	if parseErr != nil {
		closeFiles(files)
		return response{Error: parseErr.Error()}
	}
	var message frame
	if err := json.Unmarshal(payload, &message); err != nil || len(message.Identities) == 0 || len(message.Identities) > maxGrants {
		closeFiles(files)
		return response{Error: "invalid isolation registration"}
	}
	pins, err := isolation.FromFiles(message.Identities, files)
	if err != nil {
		return response{Error: err.Error()}
	}
	epoch := s.current()
	if epoch != s.origin {
		_ = pins.Close()
		return response{Error: "isolation broker control generation changed"}
	}
	select {
	case <-epoch:
		_ = pins.Close()
		return response{Error: "isolation broker control generation expired"}
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.entries)+len(s.claimed)+len(s.completed) >= maxPending {
		_ = pins.Close()
		return response{Error: "isolation broker unavailable or full"}
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		_ = pins.Close()
		return response{Error: "isolation receipt unavailable"}
	}
	receipt := hex.EncodeToString(random[:])
	entry := &pending{frame: message, pins: pins, epoch: epoch}
	entry.timer = time.AfterFunc(pendingLifetime, func() { s.expire(receipt) })
	s.entries[receipt] = entry
	return response{Receipt: receipt}
}

func (s *Server) expire(receipt string) {
	s.mu.Lock()
	entry := s.entries[receipt]
	delete(s.entries, receipt)
	s.mu.Unlock()
	if entry != nil {
		_ = entry.pins.Close()
	}
}

func (s *Server) Consume(receipt string, body []byte, reg Registration, epoch <-chan struct{}) (*isolation.Set, error) {
	s.mu.Lock()
	entry := s.entries[receipt]
	delete(s.entries, receipt)
	if entry != nil {
		entry.timer.Stop()
	}
	s.mu.Unlock()
	if entry == nil {
		return nil, errors.New("isolation receipt missing, expired, or already used")
	}
	if entry.epoch != epoch || entry.epoch != s.current() || entry.frame.Digest != sha256.Sum256(body) || !reflect.DeepEqual(entry.frame.Registration, reg) {
		_ = entry.pins.Close()
		return nil, errors.New("isolation receipt does not match authenticated create")
	}
	select {
	case <-epoch:
		_ = entry.pins.Close()
		return nil, errors.New("isolation credential rotated")
	default:
	}
	current := s.current()
	s.mu.Lock()
	if s.closed || epoch != current || epoch != s.origin {
		s.mu.Unlock()
		_ = entry.pins.Close()
		return nil, errors.New("isolation ownership changed before claim")
	}
	s.claimed[receipt] = time.AfterFunc(time.Minute, func() { s.Forget(receipt) })
	s.mu.Unlock()
	return entry.pins, nil
}

func (s *Server) Revoke() {
	s.mu.Lock()
	entries := s.entries
	s.entries = make(map[string]*pending)
	claimed := s.claimed
	s.claimed = make(map[string]*time.Timer)
	completedEntries := s.completed
	s.completed = make(map[string]*completed)
	s.mu.Unlock()
	for _, entry := range entries {
		entry.timer.Stop()
		_ = entry.pins.Close()
	}
	for _, entry := range completedEntries {
		entry.timer.Stop()
	}
	for _, timer := range claimed {
		timer.Stop()
	}
}

// CommitCreated publishes and records the outcome under one broker ownership lock.
func (s *Server) CommitCreated(receipt string, epoch <-chan struct{}, created Created, publish func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || epoch != s.origin {
		return false
	}
	select {
	case <-epoch:
		return false
	default:
	}
	timer, ok := s.claimed[receipt]
	if !ok {
		return false
	}
	publish()
	timer.Stop()
	delete(s.claimed, receipt)
	entry := &completed{created: created, epoch: epoch}
	entry.timer = time.AfterFunc(time.Minute, func() {
		s.mu.Lock()
		delete(s.completed, receipt)
		s.mu.Unlock()
	})
	s.completed[receipt] = entry
	return true
}

func (s *Server) Forget(receipt string) {
	s.mu.Lock()
	timer := s.claimed[receipt]
	delete(s.claimed, receipt)
	s.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func (s *Server) Created(receipt string, epoch <-chan struct{}) (Created, bool) {
	created, ok, _ := s.Lookup(receipt, epoch)
	return created, ok
}

// Lookup reports a completed or still-owned registration for exact receipt reconciliation.
func (s *Server) Lookup(receipt string, epoch <-chan struct{}) (Created, bool, bool) {
	current := s.current()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || epoch != current || epoch != s.origin {
		return Created{}, false, false
	}
	select {
	case <-epoch:
		return Created{}, false, false
	default:
	}
	entry, ok := s.completed[receipt]
	if ok && entry.epoch == epoch {
		return entry.created, true, false
	}
	if waiting, ok := s.entries[receipt]; ok && waiting.epoch == epoch {
		return Created{}, false, true
	}
	_, pending := s.claimed[receipt]
	return Created{}, false, pending
}

func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	err := s.conn.Close()
	<-s.done
	s.Revoke()
	return err
}
