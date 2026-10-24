//go:build !linux

package isolationbroker

import (
	"context"
	"errors"
	"os"

	"github.com/kandev/kandev/internal/agentctl/server/process/isolation"
)

var errUnsupported = errors.New("isolation descriptor broker requires Linux")

type Registration struct {
	TaskID     string
	SessionID  string
	InstanceID string
	Workspace  string
	Identities []isolation.Identity
}
type Client struct{}
type Server struct{}
type Created struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
}

func SocketPair() (*os.File, *os.File, error) { return nil, nil, errUnsupported }
func NewClient(file *os.File) (*Client, error) {
	if file != nil {
		_ = file.Close()
	}
	return nil, errUnsupported
}
func NewServer(file *os.File, _ func() <-chan struct{}) (*Server, error) {
	if file != nil {
		_ = file.Close()
	}
	return nil, errUnsupported
}
func (*Client) Close() error { return nil }
func (*Client) Register(context.Context, []byte, Registration, *isolation.Set) (string, error) {
	return "", errUnsupported
}
func (*Server) Consume(string, []byte, Registration, <-chan struct{}) (*isolation.Set, error) {
	return nil, errUnsupported
}
func (*Server) Revoke()                                         {}
func (*Server) Forget(string)                                   {}
func (*Server) MarkCreated(string, <-chan struct{}, Created)    {}
func (*Server) Created(string, <-chan struct{}) (Created, bool) { return Created{}, false }
func (*Server) Close() error                                    { return nil }
