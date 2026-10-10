// Package updatenotice is what a node's auto-update agent last found, kept in
// a file so the node report (and through it `orama status`) can show it. The
// agent runs as root and writes the file; the report collector reads it.
package updatenotice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/durablefile"
)

// Path is the notice file, beside the other release state in /etc/orama.
const Path = "/etc/orama/update-notice.json"

const (
	// filePerm: root writes, the node report reads.
	filePerm = 0o644
	// maxBytes bounds a read; a notice is a few hundred bytes.
	maxBytes = 16 << 10
)

// The states a notice can be in.
const (
	// StateAvailable: a newer verified release exists and was not installed.
	StateAvailable = "available"
	// StateRefused: a release was found and refused (it did not verify, was
	// older, or was marked bad). Reason says why.
	StateRefused = "refused"
	// StateFailed: this node tried to install a release and rolled back.
	StateFailed = "failed"
)

// Notice is the agent's last finding.
type Notice struct {
	State     string    `json:"state"`
	Mode      string    `json:"mode"`
	Channel   string    `json:"channel"`
	Current   string    `json:"current"`
	Candidate string    `json:"candidate,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Write replaces the notice at path.
func Write(path string, n Notice) error {
	switch n.State {
	case StateAvailable, StateRefused, StateFailed:
	default:
		return fmt.Errorf("update notice state %q is not available, refused or failed", n.State)
	}
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("encode the update notice: %w", err)
	}
	return durablefile.Write(path, data, filePerm)
}

// Clear removes the notice at path; there being none is not an error.
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the update notice: %w", err)
	}
	return nil
}

// Read returns the notice at path, nil when there is none. A file that does
// not parse is an error.
func Read(path string) (*Notice, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the update notice: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("read the update notice: %w", err)
	}
	var n Notice
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("parse the update notice %s: %w", path, err)
	}
	return &n, nil
}
