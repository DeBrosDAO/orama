package updateagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/durablefile"
)

const (
	// journalName is the install intent, in workDir (root's alone, on /var).
	journalName = "install-intent.json"
	// journalPerm: root writes and reads it.
	journalPerm = 0o600
	// journalMaxBytes bounds a read; an intent is a few hundred bytes.
	journalMaxBytes = 16 << 10
)

// fileJournal keeps the install intent in a file, written durably: it has to
// survive the power loss it exists for.
type fileJournal struct {
	path string
}

var _ autoupdate.Journal = fileJournal{}

func (j fileJournal) Begin(i autoupdate.Intent) error {
	pending, err := j.Pending()
	if err != nil {
		return err
	}
	if pending != nil {
		return fmt.Errorf("an install of release %s is already under way (%s)", pending.Version, j.path)
	}
	return j.Replace(i)
}

// Replace writes the intent over the one there.
func (j fileJournal) Replace(i autoupdate.Intent) error {
	data, err := json.Marshal(i)
	if err != nil {
		return fmt.Errorf("encode the install intent: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(j.path), workDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(j.path), err)
	}
	return durablefile.Write(j.path, data, journalPerm)
}

func (j fileJournal) Pending() (*autoupdate.Intent, error) {
	f, err := openNoFollow(j.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the install intent: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, journalMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("read the install intent: %w", err)
	}
	var i autoupdate.Intent
	if err := json.Unmarshal(data, &i); err != nil {
		return nil, fmt.Errorf("the install intent %s does not parse (remove it once you know the node is on the release you want): %w", j.path, err)
	}
	return &i, nil
}

func (j fileJournal) Clear() error {
	if err := os.Remove(j.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the install intent: %w", err)
	}
	return nil
}
