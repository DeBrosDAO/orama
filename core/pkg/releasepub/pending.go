package releasepub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// Asset is an archive a release uploads to GitHub.
type Asset struct {
	// Name is the asset's file name; Path is where the archive is on this
	// machine; Target is the name the targets metadata lists it under.
	Name   string `json:"name"`
	Path   string `json:"path"`
	Target string `json:"target"`
	SHA256 string `json:"sha256"`
}

// Pending is what a cut leaves for publish to upload.
type Pending struct {
	Channel string  `json:"channel"`
	Version string  `json:"version"`
	Tag     string  `json:"tag"`
	Assets  []Asset `json:"assets"`
}

// writePending records the cut's archives in the repository directory.
func writePending(repo Repo, plan *CutPlan) error {
	data, err := json.MarshalIndent(Pending{Channel: plan.Channel, Version: plan.Version, Tag: plan.Tag, Assets: plan.assets}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the pending record: %w", err)
	}
	return repo.write([]namedFile{{PendingFile, data}})
}

// ReadPending returns the pending record, or nil when there is none (a refresh
// or a root change uploads only metadata).
func (r Repo) ReadPending() (*Pending, error) {
	data, err := os.ReadFile(r.path(PendingFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", r.path(PendingFile), err)
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", r.path(PendingFile), err)
	}
	return &p, nil
}

// clearPending removes the record once its archives are uploaded.
func (r Repo) clearPending() error {
	if err := os.Remove(r.path(PendingFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the pending record: %w", err)
	}
	return nil
}

// metaFor describes signed metadata bytes the way a snapshot or timestamp
// names the file it points at: version, length and sha256.
func metaFor(version int64, data []byte) *metadata.MetaFiles {
	sum := sha256.Sum256(data)
	m := metadata.MetaFile(version)
	m.Length = int64(len(data))
	m.Hashes = metadata.Hashes{"sha256": sum[:]}
	return m
}

// fileSHA256 is the hex SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
