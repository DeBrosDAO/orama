package releasepub

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// topRoles are the four roles a root names, all with the one release key.
var topRoles = []string{metadata.ROOT, metadata.TIMESTAMP, metadata.SNAPSHOT, metadata.TARGETS}

// InitRoot makes version 1 of the repository's root: the wallet's release key
// for all four top-level roles at threshold 1, valid for RootValidity, signed
// by that key (one approval). It writes 1.root.json and root.json and returns
// the SHA-256 of the root, the digest a network manifest pins. It refuses a
// directory that already has a root.
func InitRoot(ctx context.Context, agent Agent, repo Repo, now time.Time, progress io.Writer) (string, error) {
	if _, _, err := repo.ReadRoot(); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			err = fmt.Errorf("%s already has a root; change it with renew-root, never by making a second one", repo.Dir)
		}
		return "", err
	}
	pub, err := agent.ReleaseKey(ctx)
	if err != nil {
		return "", fmt.Errorf("read the wallet's release key: %w", err)
	}
	key, err := metadata.KeyFromPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("the wallet's release key: %w", err)
	}
	expires := now.UTC().Truncate(time.Second).Add(RootValidity)
	root := metadata.Root(expires)
	root.Signed.ConsistentSnapshot = false
	for _, role := range topRoles {
		if err := root.Signed.AddKey(key, role); err != nil {
			return "", fmt.Errorf("list the release key for the %s role: %w", role, err)
		}
	}
	step := signStep{1, 1, fmt.Sprintf("root version 1, expiring %s, listing your release key for all four roles", expires.Format(time.RFC3339))}
	data, err := signRoot(ctx, agent, pub, root, step, now, progress)
	if err != nil {
		return "", err
	}
	if err := repo.write([]namedFile{{versionedRootName(1), data}, {RootFile, data}}); err != nil {
		return "", err
	}
	return releaseverify.RootDigest(data), nil
}

// RenewRoot makes the next version of the root with the same keys and a fresh
// expiry (one approval), so the root can be replaced before it expires without
// anyone being handed a new file: clients fetch <N+1>.root.json and verify it
// against the root they hold (releaseverify.Repository.UpdateRoot). It returns
// the new version and writes <N+1>.root.json and root.json.
func RenewRoot(ctx context.Context, agent Agent, repo Repo, now time.Time, progress io.Writer) (int64, error) {
	oldData, old, err := repo.ReadRoot()
	if err != nil {
		return 0, err
	}
	pub, err := agent.ReleaseKey(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the wallet's release key: %w", err)
	}
	if err := checkAgentKey(old, pub, metadata.ROOT); err != nil {
		return 0, err
	}
	next, err := metadata.Root().FromBytes(oldData)
	if err != nil {
		return 0, fmt.Errorf("copy the root: %w", err)
	}
	next.Signatures = nil
	next.Signed.Version = old.Signed.Version + 1
	next.Signed.Expires = now.UTC().Truncate(time.Second).Add(RootValidity)
	if !next.Signed.Expires.After(old.Signed.Expires) {
		return 0, fmt.Errorf("the root is valid until %s already; a renewal would not extend it", old.Signed.Expires.Format(time.RFC3339))
	}
	step := signStep{1, 1, fmt.Sprintf("root version %d, the same keys, expiring %s", next.Signed.Version, next.Signed.Expires.Format(time.RFC3339))}
	data, err := signRoot(ctx, agent, pub, next, step, now, progress)
	if err != nil {
		return 0, err
	}
	if err := checkRotation(oldData, data); err != nil {
		return 0, err
	}
	if err := repo.write([]namedFile{{versionedRootName(next.Signed.Version), data}, {RootFile, data}}); err != nil {
		return 0, err
	}
	return next.Signed.Version, nil
}

// signRoot signs root and returns bytes a client accepts as a root.
func signRoot(ctx context.Context, agent Agent, pub ed25519.PublicKey, root *metadata.Metadata[metadata.RootType], step signStep, now time.Time, progress io.Writer) ([]byte, error) {
	if err := signMetadata(ctx, agent, pub, root, step, progress); err != nil {
		return nil, err
	}
	data, err := root.ToBytes(false)
	if err != nil {
		return nil, fmt.Errorf("encode the root: %w", err)
	}
	if _, err := releaseverify.ValidateRoot(data, now); err != nil {
		return nil, fmt.Errorf("the signed root would not be usable by a client: %w", err)
	}
	return data, nil
}

// checkRotation holds next to what a client does with it: accept it as the
// version after old.
func checkRotation(old, next []byte) error {
	trusted, err := trustedmetadata.New(old)
	if err != nil {
		return fmt.Errorf("read the root being replaced: %w", err)
	}
	if _, err := trusted.UpdateRoot(next); err != nil {
		return fmt.Errorf("a client holding the current root would refuse the new one: %w", err)
	}
	return nil
}
