package isolation

import "os"

// Identity is the server-authored object binding carried through the control API.
// For a missing lock file, ParentDev/ParentIno bind the existing parent instead.
type Identity struct {
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Dev        uint64 `json:"dev,omitempty"`
	Ino        uint64 `json:"ino,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
	ParentPath string `json:"parent_path"`
	ParentDev  uint64 `json:"parent_dev"`
	ParentIno  uint64 `json:"parent_ino"`
}

// Pinned holds a source object, or the parent of an absent leaf, until teardown.
type Pinned struct {
	identity Identity
	file     *os.File
	parent   *os.File
	leaf     string
}

func (p *Pinned) File() *os.File { return p.file }
func (p *Pinned) Leaf() string   { return p.leaf }
func (p *Pinned) Close() error {
	err := p.file.Close()
	if p.parent != nil {
		if parentErr := p.parent.Close(); err == nil {
			err = parentErr
		}
	}
	return err
}
