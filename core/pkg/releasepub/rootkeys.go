package releasepub

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

const (
	// WalletKey, in a key list, stands for the wallet's own release key.
	WalletKey = "wallet"
	// maxKeysPerRole bounds a role's key list: a root is read by every client.
	maxKeysPerRole = 16
	// maxRootKeysFileBytes bounds a key-spec file.
	maxRootKeysFileBytes = 64 << 10
)

// RoleKeys are the keys that hold one top-level role and how many of them must sign.
// The zero value is the wallet's release key alone, at threshold 1.
type RoleKeys struct {
	// Keys are ed25519 public keys; empty means the wallet's release key.
	Keys []ed25519.PublicKey
	// Threshold is how many of Keys must sign; 0 means 1.
	Threshold int
}

// RootKeys says which keys hold which of the four top-level roles of a new root. The zero value is
// today's layout: the wallet's one release key holds all four roles at threshold 1. A custody split
// (a key of its own per role, or a threshold above 1 for the roles that are not signed in this
// command) is a key-spec file away, not a code change.
type RootKeys struct {
	Root, Targets, Snapshot, Timestamp RoleKeys
}

// role is the role of a TUF root named name.
func (k RootKeys) role(name string) RoleKeys {
	switch name {
	case metadata.ROOT:
		return k.Root
	case metadata.TARGETS:
		return k.Targets
	case metadata.SNAPSHOT:
		return k.Snapshot
	default:
		return k.Timestamp
	}
}

// resolve fills in the wallet's key where a role names none, and checks the layout against what
// this command can sign: it signs the root once, with the wallet's key, so the wallet's key must
// be one of the root role's keys and the root role's threshold must be 1. (More root signers need
// the other holders' signatures, which this command cannot collect.)
func (k RootKeys) resolve(wallet ed25519.PublicKey) (map[string]RoleKeys, error) {
	out := map[string]RoleKeys{}
	for _, name := range topRoles {
		r := k.role(name)
		if len(r.Keys) == 0 {
			r.Keys = []ed25519.PublicKey{wallet}
		}
		if r.Threshold == 0 {
			r.Threshold = 1
		}
		if err := checkRoleKeys(name, r); err != nil {
			return nil, err
		}
		out[name] = r
	}
	root := out[metadata.ROOT]
	if root.Threshold != 1 {
		return nil, fmt.Errorf("the root role has threshold %d, but this command signs the root once, with your release key: "+
			"a root that needs more signatures has to be signed by the other key holders, which init-root cannot collect", root.Threshold)
	}
	holdsRoot := false
	for _, key := range root.Keys {
		holdsRoot = holdsRoot || bytes.Equal(key, wallet)
	}
	if !holdsRoot {
		return nil, errors.New("your wallet's release key is not one of the root role's keys, so it cannot sign the root: " +
			`list "wallet" among the root keys`)
	}
	return out, nil
}

func checkRoleKeys(role string, r RoleKeys) error {
	if len(r.Keys) > maxKeysPerRole {
		return fmt.Errorf("the %s role lists %d keys; at most %d are allowed", role, len(r.Keys), maxKeysPerRole)
	}
	seen := map[string]bool{}
	for _, key := range r.Keys {
		id, err := KeyID(key)
		if err != nil {
			return fmt.Errorf("the %s role: %w", role, err)
		}
		if seen[id] {
			return fmt.Errorf("the %s role lists the same key twice (%s)", role, id)
		}
		seen[id] = true
	}
	if r.Threshold < 1 || r.Threshold > len(r.Keys) {
		return fmt.Errorf("the %s role has threshold %d with %d key(s): it has to be between 1 and the number of keys", role, r.Threshold, len(r.Keys))
	}
	return nil
}

// rootKeysFile is the JSON of a key-spec file:
//
//	{"root": {"keys": ["wallet"]}, "targets": {"keys": ["<64 hex>", "<64 hex>"], "threshold": 2}, ...}
//
// A role that is left out is the wallet's key at threshold 1.
type rootKeysFile struct {
	Root      *roleKeysFile `json:"root"`
	Targets   *roleKeysFile `json:"targets"`
	Snapshot  *roleKeysFile `json:"snapshot"`
	Timestamp *roleKeysFile `json:"timestamp"`
}

type roleKeysFile struct {
	Keys      []string `json:"keys"`
	Threshold int      `json:"threshold"`
}

// ParseRootKeys reads a key-spec file. Each key is 64 hex digits of an ed25519 public key, or
// "wallet" for the wallet's own release key (which init-root fills in). An unknown field, a
// second JSON value or a malformed key is an error.
func ParseRootKeys(r io.Reader, wallet ed25519.PublicKey) (RootKeys, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxRootKeysFileBytes+1))
	if err != nil {
		return RootKeys{}, fmt.Errorf("read the key file: %w", err)
	}
	if len(data) > maxRootKeysFileBytes {
		return RootKeys{}, fmt.Errorf("the key file is over %d bytes", maxRootKeysFileBytes)
	}
	var doc rootKeysFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return RootKeys{}, fmt.Errorf("the key file is not the JSON expected: %w", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return RootKeys{}, errors.New("the key file has data after its JSON object")
	}
	var out RootKeys
	for _, p := range []struct {
		name string
		in   *roleKeysFile
		out  *RoleKeys
	}{
		{metadata.ROOT, doc.Root, &out.Root}, {metadata.TARGETS, doc.Targets, &out.Targets},
		{metadata.SNAPSHOT, doc.Snapshot, &out.Snapshot}, {metadata.TIMESTAMP, doc.Timestamp, &out.Timestamp},
	} {
		if p.in == nil {
			continue
		}
		if *p.out, err = p.in.parse(wallet); err != nil {
			return RootKeys{}, fmt.Errorf("the %s role: %w", p.name, err)
		}
	}
	return out, nil
}

func (f roleKeysFile) parse(wallet ed25519.PublicKey) (RoleKeys, error) {
	out := RoleKeys{Threshold: f.Threshold}
	for _, s := range f.Keys {
		if s == WalletKey {
			out.Keys = append(out.Keys, wallet)
			continue
		}
		raw, err := hex.DecodeString(strings.ToLower(s))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return RoleKeys{}, fmt.Errorf("%q is not 64 hex digits of an ed25519 public key (or %q)", s, WalletKey)
		}
		out.Keys = append(out.Keys, ed25519.PublicKey(raw))
	}
	return out, nil
}
