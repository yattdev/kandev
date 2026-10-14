package taskpreview

import "os"

// The reviewed ingress is Linux-host only; other platforms fail closed.
func privateDirectory(string) error        { return ErrDenied }
func privateFile(string) (*os.File, error) { return nil, ErrDenied }
