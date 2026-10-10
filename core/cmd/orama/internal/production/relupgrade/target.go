// Package relupgrade is `orama upgrade`: it fetches the newest signed release
// of the active network's channel, shows what each node would go through,
// stages the verified archive on every node, and then runs the existing rolling
// upgrade (production/upgrade) one node at a time, the co-located global layer
// included.
package relupgrade

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
)

const (
	// releasesDirName is the directory of the CLI's config directory that
	// keeps, per network, the rollback record and the work area of a fetch.
	releasesDirName = "releases"
	// workDirName, seenFileName and adoptedRootFileName are inside it.
	workDirName         = "work"
	seenFileName        = "release-seen.json"
	adoptedRootFileName = "release-root-adopted.json"
)

// Target is the release channel a cluster is upgraded from: the registry
// network its environment runs on.
type Target struct {
	Network  string
	Manifest *netregistry.Manifest
	// Root is the release root built into this CLI for the network, already
	// checked against the manifest's pin.
	Root []byte
}

// ResolveTarget finds the release channel env runs on. An environment that is
// on no registry network has no channel to upgrade from, and says how to
// upgrade it by hand.
func ResolveTarget(env *cli.Environment, registry *netregistry.Registry) (Target, error) {
	if env.Network == "" {
		return Target{}, fmt.Errorf("the %q environment is not on a network of the registry, so there is no release channel to upgrade it from; "+
			"to upgrade a cluster from a build of your own, use `orama maint rollout --env %s`", env.Name, env.Name)
	}
	n, err := registry.Get(env.Network)
	if err != nil {
		return Target{}, fmt.Errorf("the %q environment runs on network %q: %w", env.Name, env.Network, err)
	}
	return Target{Network: n.Manifest.Name, Manifest: n.Manifest, Root: n.Root}, nil
}

// FetchParams are the parameters for fetching t's newest release for arch.
// home is the CLI's config directory; the rollback record is kept per network
// beneath it, so an older snapshot cannot be replayed to this machine.
func (t Target) FetchParams(home, arch string, now time.Time) releasefetch.Params {
	dir := filepath.Join(home, releasesDirName, t.Network)
	return releasefetch.Params{
		RepoURL:     t.Manifest.ReleaseRepo,
		Channel:     t.Manifest.Channel,
		Arch:        arch,
		Root:        t.Root,
		RootSHA256:  t.Manifest.ReleaseRootSHA256,
		MinVersion:  t.Manifest.MinVersion,
		WorkDir:     filepath.Join(dir, workDirName),
		SeenPath:    filepath.Join(dir, seenFileName),
		AdoptedRoot: filepath.Join(dir, adoptedRootFileName),
		Now:         now,
	}
}
