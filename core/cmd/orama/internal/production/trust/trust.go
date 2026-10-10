// Package trust implements `orama node trust`: what this node accepts code
// from besides its operator's wallet.
package trust

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// maxRootBytes bounds the root file read from disk; a root is a few kilobytes.
const maxRootBytes = 1 << 20

// AddRootOptions are the flags of `orama node trust add-root`.
type AddRootOptions struct {
	// File is the root.json to adopt.
	File string
	// Replace allows adopting a root when a different one is adopted already.
	Replace bool
	// Rotate adopts the root as the next version of the one adopted, checked against it the way
	// a client following the repository would (releaseverify.RotateRoot). It is how a root the
	// operator pushed with a release replaces the one the node holds without --replace.
	Rotate bool
}

// AddRoot adopts the TUF root in opts.File as this node's release root
// (releaseverify.RootPath). From then on the node accepts releases signed
// under it, for `orama maint node stage-archive --release-only`, and the cluster's
// auto-update agent follows its channel.
func AddRoot(opts AddRootOptions, out io.Writer) error {
	if err := clierr.RequireRoot("adopting a release root"); err != nil {
		return err
	}
	return addRoot(opts, releaseverify.RootPath, releaseverify.SeenPath, time.Now(), out)
}

func addRoot(opts AddRootOptions, rootPath, seenPath string, now time.Time, out io.Writer) error {
	data, err := readRootFile(opts.File)
	if err != nil {
		return err
	}
	if opts.Rotate {
		return rotateRoot(releaseverify.RootUpdate{RootPath: rootPath, SeenPath: seenPath, Now: now}, data, out)
	}
	digest, err := releaseverify.ValidateRoot(data, now)
	if err != nil {
		return clierr.Usage("%s: %v", opts.File, err)
	}
	current, err := releaseverify.ReadRoot(rootPath)
	switch {
	case err == nil && releaseverify.RootDigest(current) != digest && !opts.Replace:
		return clierr.Usage("this node already trusts the release root %s; adopting %s replaces it, so pass --replace "+
			"once you have checked the new root's digest with the other operators",
			releaseverify.RootDigest(current), digest)
	case err != nil && !errors.Is(err, releaseverify.ErrNoRoot):
		return err
	}
	changed, err := releaseverify.AdoptRoot(rootPath, data, now)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintf(out, "Release root %s is already adopted.\n", digest)
		return nil
	}
	fmt.Fprintf(out, "Adopted release root %s (%s).\n", digest, rootPath)
	fmt.Fprintln(out, "Releases signed under it are now accepted by 'orama maint node stage-archive --release-only' on this node.")
	fmt.Fprintln(out, "To give every node of the cluster the same root, build with --release-root and push the archive.")
	return nil
}

// rotateRoot adopts data as the next version of the adopted root, or says why it is not.
func rotateRoot(u releaseverify.RootUpdate, data []byte, out io.Writer) error {
	changed, err := releaseverify.RotateRoot(u, data)
	switch {
	case errors.Is(err, releaseverify.ErrNoRoot):
		return clierr.Usage("this node has no release root to rotate from: adopt one with 'orama node trust add-root <root.json>'")
	case errors.Is(err, releaseverify.ErrRootRotation):
		return clierr.Usage("the root is not the next version of the one this node trusts, so it is not adopted: %v\n"+
			"  A node more than one version behind follows the rotations through its release repository (the auto-update agent), or takes one root per push", err)
	case err != nil:
		return err
	case !changed:
		fmt.Fprintf(out, "Release root %s is already adopted.\n", releaseverify.RootDigest(data))
		return nil
	}
	fmt.Fprintf(out, "Rotated the release root to %s (%s).\n", releaseverify.RootDigest(data), u.RootPath)
	return nil
}

func readRootFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, clierr.Usage("read the release root: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRootBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxRootBytes {
		return nil, clierr.Usage("%s is over %d bytes; it is not a TUF root", path, maxRootBytes)
	}
	return data, nil
}
