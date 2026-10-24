//go:build !linux

package isolation

import "errors"

var errUnsupported = errors.New("isolation object pinning requires Linux")

func Capture(string) (Identity, error) { return Identity{}, errUnsupported }
func Pin(Identity) (*Pinned, error)    { return nil, errUnsupported }
func (*Pinned) Validate() error        { return errUnsupported }
