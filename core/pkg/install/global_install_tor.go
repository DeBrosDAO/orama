package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// torDirMode is a Tor DataDirectory: Tor refuses one others can read.
	torDirMode = 0o700
	// torFileMode is every key.
	torFileMode = 0o600
	// torrcMode is the torrc: root's, readable by the role's account.
	torrcMode = 0o644
	// torKeyLimit bounds a key file read from the ceremony bundle.
	torKeyLimit = 64 << 10
	// torRejectLimit bounds the exit operator's reject list read back.
	torRejectLimit = 1 << 20
	torKeysSubdir  = "keys"
)

// requiredDirauthKeys must be in a directory authority's key bundle. The first
// two are the authority's signing key and its certificate; the rest are the
// relay identity, which is the authority's name in every DirAuthority line.
var requiredDirauthKeys = []string{
	tornet.KeyAuthoritySigning, tornet.KeyAuthorityCert,
	tornet.KeyRelayIdentity, tornet.KeyEd25519Master, tornet.KeyEd25519MasterPub,
}

// optionalDirauthKeys are copied when the bundle has them; tor makes the rest itself.
var optionalDirauthKeys = []string{
	"ed25519_signing_secret_key", "ed25519_signing_cert", "secret_onion_key", "secret_onion_key_ntor",
}

// identityKeys may not change under a running authority: replacing one gives
// the host a new identity in the network.
var identityKeys = []string{tornet.KeyRelayIdentity, tornet.KeyEd25519Master}

// TorOptions is what the Tor roles need beyond their service names.
type TorOptions struct {
	// Exit makes the relay an exit. The network file must allow exits.
	Exit bool
	// Address is the public IPv4 address the relay or authority publishes. A
	// host in the co-located namespace cannot discover it.
	Address string
	// Contact is published in the relay descriptor: how to reach the operator.
	Contact string
	// NodeID is the on-chain node id the relay's nickname is derived from.
	NodeID string
	// BandwidthMbit limits what the relay carries for others, each way. Zero is unlimited.
	BandwidthMbit uint
	// Family are the RSA fingerprints of the operator's other relays.
	Family []string
	// DirauthKeysDir is the authority's bundle from the key ceremony
	// (deploy/<nickname>): its keys/ directory holds the keys to install.
	DirauthKeysDir string
}

func (t TorOptions) validate(services []GlobalService) error {
	relay, dirauth, onion := slices.Contains(services, GlobalServiceRelay), slices.Contains(services, GlobalServiceDirauth), slices.Contains(services, GlobalServiceOnion)
	publishes := relay || dirauth
	set := t.Exit || t.Address != "" || t.Contact != "" || t.NodeID != "" || t.BandwidthMbit != 0 || len(t.Family) > 0 || t.DirauthKeysDir != ""
	switch {
	case !publishes && !onion && set:
		return errors.New("the --tor-* options apply only with the relay, dirauth or onion service")
	case !publishes && set:
		return errors.New("the onion service takes no --tor-* option: it publishes no relay")
	case t.Exit && !relay:
		return errors.New("the exit role is a relay with an exit policy: use --services relay,exit")
	case relay && t.NodeID == "":
		return errors.New("a relay's nickname is derived from its on-chain node id: give --tor-node-id")
	case !relay && t.NodeID != "":
		return errors.New("--tor-node-id applies only with the relay service: an authority's nickname is the one in the network file")
	case dirauth && t.DirauthKeysDir == "":
		return errors.New("a directory authority needs its key bundle from the key ceremony: give --tor-authority-keys")
	case !dirauth && t.DirauthKeysDir != "":
		return errors.New("--tor-authority-keys applies only with the dirauth service")
	case publishes && (t.Address == "" || t.Contact == ""):
		return errors.New("a relay or directory authority publishes its public address and contact: give --tor-address and --tor-contact")
	}
	return nil
}

// torPlan is everything the Tor roles install, decided before the host changes.
type torPlan struct {
	network     tornet.Network
	networkJSON []byte
	instances   []torInstance
}

// torInstance is one tor process's DataDirectory, owned by the account of its role.
type torInstance struct {
	home  string
	user  string
	torrc string
	keys  []torKey
}

type torKey struct {
	name string
	data []byte
	// keepInstalled leaves a file tor has already written alone: tor rotates its
	// signing and onion keys itself, and an install must not put the
	// ceremony-time copies back over the live ones.
	keepInstalled bool
}

// planGlobalTor reads the network file from the staged directory and renders
// every torrc, so a bad network file, a refused exit or a wrong key bundle
// stops the install before anything on the host has changed. It returns nil
// when the install has no Tor role.
func planGlobalTor(h GlobalHost, opts GlobalInstallOptions) (*torPlan, error) {
	relay, dirauth, onion := slices.Contains(opts.Services, GlobalServiceRelay), slices.Contains(opts.Services, GlobalServiceDirauth), slices.Contains(opts.Services, GlobalServiceOnion)
	if !relay && !dirauth && !onion {
		return nil, nil
	}
	raw, err := rootfs.At(opts.StagedDir).ReadFile(filepath.Join(opts.StagedDir, constants.TorNetworkFile), tornet.NetworkFileLimit)
	if err != nil {
		return nil, fmt.Errorf("read the Tor network file (put the network's %s in %s): %w", constants.TorNetworkFile, opts.StagedDir, err)
	}
	network, err := tornet.ParseNetwork(raw)
	if err != nil {
		return nil, err
	}
	if err := requireOneTorPublisher(h, opts); err != nil {
		return nil, err
	}
	plan := &torPlan{network: network, networkJSON: raw}
	if relay {
		inst, err := planTorRelay(h, opts.Tor, network)
		if err != nil {
			return nil, err
		}
		plan.instances = append(plan.instances, inst)
	}
	if dirauth {
		inst, err := planTorDirauth(opts.Tor, network)
		if err != nil {
			return nil, err
		}
		plan.instances = append(plan.instances, inst)
	}
	if onion {
		torrc, err := tornet.OnionTorrc(tornet.OnionConfig{Network: network, Home: constants.GlobalTorOnionHome, Target: txGateListen})
		if err != nil {
			return nil, fmt.Errorf("render the onion service's torrc: %w", err)
		}
		plan.instances = append(plan.instances, torInstance{home: constants.GlobalTorOnionHome, user: globalTorOnionUser, torrc: torrc})
	}
	return plan, nil
}

// requireOneTorPublisher refuses a relay on a host that is a directory authority
// and the reverse, whichever install made the first: both publish the ORPort.
func requireOneTorPublisher(h GlobalHost, opts GlobalInstallOptions) error {
	other := map[GlobalService]GlobalService{GlobalServiceRelay: GlobalServiceDirauth, GlobalServiceDirauth: GlobalServiceRelay}
	for s, o := range other {
		if !slices.Contains(opts.Services, s) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(h.UnitDir, globalServiceSpecs[o].unit)); err == nil {
			return fmt.Errorf("this host already runs the %s role, and a directory authority is a relay: it cannot also take the %s role on the same ORPort", o, s)
		}
	}
	return nil
}

func planTorRelay(h GlobalHost, t TorOptions, network tornet.Network) (torInstance, error) {
	var reject []string
	if t.Exit {
		var err error
		if reject, err = readExitRejectList(h); err != nil {
			return torInstance{}, err
		}
	}
	torrc, err := tornet.RelayTorrc(tornet.RelayConfig{
		Network: network, Home: constants.GlobalTorRelayHome, Nickname: tornet.NicknameFor(t.NodeID),
		Contact: t.Contact, Address: t.Address, ORPort: constants.GlobalTorORPort,
		Exit: t.Exit, ExitReject: reject, Family: t.Family, BandwidthMbit: t.BandwidthMbit,
	})
	if err != nil {
		return torInstance{}, fmt.Errorf("render the relay's torrc: %w", err)
	}
	return torInstance{home: constants.GlobalTorRelayHome, user: globalTorRelayUser, torrc: torrc}, nil
}

func planTorDirauth(t TorOptions, network tornet.Network) (torInstance, error) {
	auth, ok := network.AuthorityAt(t.Address)
	if !ok {
		return torInstance{}, fmt.Errorf("--tor-address %s is not a directory authority of network %s; its authorities are published at the addresses in the network file", t.Address, network.Name)
	}
	if auth.ORPort != constants.GlobalTorORPort || auth.DirPort != constants.GlobalTorDirPort {
		return torInstance{}, fmt.Errorf("authority %s is published on ports %d/%d, but the global firewall opens %d/%d", auth.Nickname, auth.ORPort, auth.DirPort, constants.GlobalTorORPort, constants.GlobalTorDirPort)
	}
	keys, err := readDirauthKeys(t.DirauthKeysDir, auth)
	if err != nil {
		return torInstance{}, err
	}
	torrc, err := tornet.RelayTorrc(tornet.RelayConfig{
		Network: network, Home: constants.GlobalTorDirauthHome, Nickname: auth.Nickname,
		Contact: t.Contact, Address: t.Address, ORPort: auth.ORPort, DirPort: auth.DirPort,
		Authority: true, Family: t.Family, BandwidthMbit: t.BandwidthMbit,
	})
	if err != nil {
		return torInstance{}, fmt.Errorf("render the directory authority's torrc: %w", err)
	}
	return torInstance{home: constants.GlobalTorDirauthHome, user: globalTorDirauthUser, torrc: torrc, keys: keys}, nil
}

// readDirauthKeys reads the authority's bundle and checks it is this
// authority's: the relay identity key must hash to the fingerprint the network
// file publishes and the certificate must carry the published v3 identity, so a
// bundle made for another host is refused here and not found by the other
// authorities refusing to vote with this one.
func readDirauthKeys(dir string, auth tornet.Authority) ([]torKey, error) {
	root := rootfs.At(dir)
	var keys []torKey
	for _, name := range append(slices.Clone(requiredDirauthKeys), optionalDirauthKeys...) {
		data, err := root.ReadFile(filepath.Join(dir, torKeysSubdir, name), torKeyLimit)
		if errors.Is(err, fs.ErrNotExist) && slices.Contains(optionalDirauthKeys, name) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s of the authority key bundle %s: %w", name, dir, err)
		}
		keys = append(keys, torKey{name: name, data: data, keepInstalled: slices.Contains(optionalDirauthKeys, name)})
	}
	byName := map[string][]byte{}
	for _, k := range keys {
		byName[k.name] = k.data
	}
	fp, err := tornet.RelayFingerprint(byName[tornet.KeyRelayIdentity])
	if err != nil {
		return nil, err
	}
	if fp != auth.Fingerprint {
		return nil, fmt.Errorf("the relay identity key in %s hashes to %s, but authority %s is published as %s: this is another authority's bundle", dir, fp, auth.Nickname, auth.Fingerprint)
	}
	ed, err := tornet.Ed25519Identity(byName[tornet.KeyEd25519MasterPub])
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", tornet.KeyEd25519MasterPub, dir, err)
	}
	if ed != auth.Ed25519ID {
		return nil, fmt.Errorf("the ed25519 identity in %s is %s, but authority %s is published as %s: this is another authority's bundle", dir, ed, auth.Nickname, auth.Ed25519ID)
	}
	v3, expires, err := tornet.ParseAuthorityCertificate(string(byName[tornet.KeyAuthorityCert]))
	if err != nil {
		return nil, fmt.Errorf("the certificate in %s: %w", dir, err)
	}
	if !expires.After(time.Now()) {
		return nil, fmt.Errorf("the signing certificate in %s expired on %s: renew it offline (docs/TOR_NETWORK.md, Rotating a signing certificate)", dir, expires.Format(time.DateOnly))
	}
	if v3 != auth.V3Ident {
		return nil, fmt.Errorf("the certificate in %s is for authority identity %s, but authority %s is published as %s", dir, v3, auth.Nickname, auth.V3Ident)
	}
	return keys, nil
}

// readExitRejectList is the exit operator's list of refused destinations. A
// missing file is no list.
func readExitRejectList(h GlobalHost) ([]string, error) {
	path := filepath.Join(h.StateDir, constants.GlobalTorExitRejectFile)
	data, err := h.StateRoot.ReadFile(path, torRejectLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the exit reject list %s: %w", path, err)
	}
	rules, err := tornet.ParseExitRejectList(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rules, nil
}

// applyGlobalTor writes the network file, then each tor process's
// DataDirectory: owned by the Tor account with mode 0700, the torrc, and for a
// directory authority its keys. The identity keys of a host that already has
// them are never replaced.
func applyGlobalTor(h GlobalHost, plan *torPlan) error {
	netPath := filepath.Join(h.StateDir, constants.GlobalTorAuthoritiesFile)
	if err := h.StateRoot.WriteFile(netPath, plan.networkJSON, globalUnitMode); err != nil {
		return fmt.Errorf("write %s: %w", netPath, err)
	}
	// Every identity check comes before the first write, so a refusal leaves the
	// installed keys exactly as they were.
	for _, inst := range plan.instances {
		for _, k := range inst.keys {
			if err := keepIdentity(h, filepath.Join(h.StateDir, filepath.Base(inst.home), torKeysSubdir, k.name), k); err != nil {
				return err
			}
		}
	}
	for _, inst := range plan.instances {
		uid, gid, err := h.Lookup(inst.user)
		if err != nil {
			return fmt.Errorf("look up the %s account: %w", inst.user, err)
		}
		home := filepath.Join(h.StateDir, filepath.Base(inst.home))
		if err := ownedDir(h, home, uid, gid); err != nil {
			return err
		}
		// The torrc is root's and sits beside the DataDirectory, not in it.
		torrc := filepath.Join(h.StateDir, filepath.Base(constants.GlobalTorrcFor(inst.home)))
		if err := h.StateRoot.WriteFile(torrc, []byte(inst.torrc), torrcMode); err != nil {
			return fmt.Errorf("write %s: %w", torrc, err)
		}
		if len(inst.keys) == 0 {
			continue
		}
		keysDir := filepath.Join(home, torKeysSubdir)
		if err := ownedDir(h, keysDir, uid, gid); err != nil {
			return err
		}
		for _, k := range inst.keys {
			path := filepath.Join(keysDir, k.name)
			if k.keepInstalled {
				if _, err := h.StateRoot.ReadFile(path, torKeyLimit); err == nil {
					continue
				} else if !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("read %s: %w", path, err)
				}
			}
			if err := ownedFile(h, path, k.data, uid, gid); err != nil {
				return err
			}
		}
		h.Logf("  ✓ %s configured (%d key files)", inst.home, len(inst.keys))
	}
	return nil
}

// keepIdentity refuses to replace an installed identity key with different bytes.
func keepIdentity(h GlobalHost, path string, k torKey) error {
	if !slices.Contains(identityKeys, k.name) {
		return nil
	}
	old, err := h.StateRoot.ReadFile(path, torKeyLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Equal(old, k.data) {
		return fmt.Errorf("%s already holds a different %s: replacing it would give this host a new identity in the network; remove the file yourself if that is what you want", path, k.name)
	}
	return nil
}

func ownedDir(h GlobalHost, dir string, uid, gid int) error {
	if err := h.StateRoot.MkdirAll(dir, torDirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := h.Chown(h.StateRoot, dir, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", dir, err)
	}
	if err := h.StateRoot.Chmod(dir, torDirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

func ownedFile(h GlobalHost, path string, data []byte, uid, gid int) error {
	if err := h.StateRoot.WriteFile(path, data, torFileMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := h.Chown(h.StateRoot, path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	if err := h.StateRoot.Chmod(path, torFileMode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// logfWriter turns the installer's log function into the io.Writer the Tor
// installer prints its progress to.
type logfWriter struct {
	logf func(format string, args ...any)
}

func (w logfWriter) Write(p []byte) (int, error) {
	if w.logf != nil {
		w.logf("%s", strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}
