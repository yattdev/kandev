//go:build !linux

package isolation

import (
	"errors"
	"os"
)

var errUnsupported = errors.New("isolation object pinning requires Linux")

func Capture(string) (Identity, error)                { return Identity{}, errUnsupported }
func CapturePinned(string) (Identity, *Pinned, error) { return Identity{}, nil, errUnsupported }
func Pin(Identity) (*Pinned, error)                   { return nil, errUnsupported }
func (*Pinned) Validate() error                       { return errUnsupported }
func FromFiles(_ []Identity, files []*os.File) (*Set, error) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
	return nil, errUnsupported
}
