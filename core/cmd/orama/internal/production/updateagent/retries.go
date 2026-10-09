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
	// retryName is the release this node could not start installing, in
	// workDir beside the install intent.
	retryName = "install-retry.json"
	// retryPerm: root writes and reads it.
	retryPerm = 0o600
	// retryMaxBytes bounds a read; a retry record is a few hundred bytes.
	retryMaxBytes = 16 << 10
)

// fileRetries keeps the retry record in a file: the backoff has to outlive the
// run that set it, since every tick is a new process.
type fileRetries struct {
	path string
}

var _ autoupdate.Retries = fileRetries{}

func (r fileRetries) Current() (*autoupdate.Retry, error) {
	f, err := openNoFollow(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the install retry record: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, retryMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("read the install retry record: %w", err)
	}
	var v autoupdate.Retry
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("the install retry record %s does not parse (remove it to have the release tried again at once): %w", r.path, err)
	}
	return &v, nil
}

func (r fileRetries) Set(v autoupdate.Retry) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode the install retry record: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), workDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(r.path), err)
	}
	return durablefile.Write(r.path, data, retryPerm)
}

func (r fileRetries) Clear() error {
	if err := os.Remove(r.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the install retry record: %w", err)
	}
	return nil
}
