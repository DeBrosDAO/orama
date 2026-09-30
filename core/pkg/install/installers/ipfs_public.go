package installers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// Public Kubo is a second daemon. It is not the cluster's private swarm:
// there is no swarm.key, private ranges are filtered rather than cleared,
// and it announces only what a deal pinned. Kubo (v0.38 and later) reads Provide.Strategy
// (the Reprovider.Strategy key is the older name and is not written).

// publicSwarmFilters are the ranges a public daemon must not dial or announce.
// The private installer clears AddrFilters so the WireGuard mesh is reachable.
// This list puts that mesh, and the other non-public ranges, back.
var publicSwarmFilters = []string{
	"/ip4/10.0.0.0/ipcidr/8",
	"/ip4/100.64.0.0/ipcidr/10",
	"/ip4/127.0.0.0/ipcidr/8",
	"/ip4/169.254.0.0/ipcidr/16",
	"/ip4/172.16.0.0/ipcidr/12",
	"/ip4/192.168.0.0/ipcidr/16",
	"/ip6/fc00::/ipcidr/7",
	"/ip6/fe80::/ipcidr/10",
}

// PublicAPITokenFile is the bearer file next to the public repo. Mode 0640,
// group orama-ipfs-pub-rpc, so the provider can read it and other users cannot.
const PublicAPITokenFile = constants.GlobalIPFSAPITokenFile

// PublicAPIAllowedPaths are the only RPC calls the bearer allows: what the
// provider needs (add, cat, pin/add, pin/rm) and the GC timer (repo/gc). The
// token can not read or change the config, the peer key or the swarm.
var PublicAPIAllowedPaths = []string{
	"/api/v0/add", "/api/v0/cat", "/api/v0/pin/add", "/api/v0/pin/rm", "/api/v0/repo/gc",
}

// PublicDenylistFile is the CID denylist the provider checks before it accepts
// a deal. Kubo itself does not read it.
const PublicDenylistFile = "denylist"

// NewPublicKuboToken is a fresh RPC bearer. It is not derived from a cluster
// secret: a global node does not have one.
func NewPublicKuboToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("public kubo token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// PublicStorageMax is declared capacity plus ten percent, in Kubo's GB form.
// A declaration under a gibibyte still gets 1GB so the field is not "0GB".
func PublicStorageMax(declaredBytes uint64) string {
	withHeadroom := declaredBytes + declaredBytes/10
	const gib = uint64(1_000_000_000)
	gb := withHeadroom / gib
	if gb < 1 {
		gb = 1
	}
	return fmt.Sprintf("%dGB", gb)
}

// PublicKuboConfig builds a repo config. existing may be nil or a config
// ipfs init already wrote; Identity is kept when it is present. token is the
// RPC bearer. An empty token is refused. apiHost is the address the RPC
// listens on; it overwrites the API address of an existing config.
func PublicKuboConfig(existing []byte, token string, declaredBytes uint64, apiHost string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("public kubo token is empty")
	}
	if strings.Contains(token, "\n") || strings.Contains(token, ":") {
		return nil, fmt.Errorf("public kubo token has a character the bearer line cannot carry")
	}

	config := map[string]interface{}{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &config); err != nil {
			return nil, fmt.Errorf("parse public kubo config: %w", err)
		}
	}
	// The sections below are merged into what ipfs init wrote, key by key:
	// replacing a whole section drops what Kubo requires, such as
	// Datastore.Spec, and the connection-manager defaults of the server
	// profile.
	sections, err := configSections(config, "Addresses", "Swarm", "API", "Provide", "Routing", "Datastore")
	if err != nil {
		return nil, err
	}
	setPublicSections(sections, token, declaredBytes, apiHost)

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal public kubo config: %w", err)
	}
	if strings.Contains(string(out), "swarm.key") {
		return nil, fmt.Errorf("public kubo config named a swarm key")
	}
	return out, nil
}

// configSections returns config's sections named by keys as maps, creating
// the absent ones. A section that is present but not an object is an error:
// merging into it is impossible and replacing it would drop what it holds.
func configSections(config map[string]interface{}, keys ...string) (map[string]map[string]interface{}, error) {
	sections := make(map[string]map[string]interface{}, len(keys))
	for _, key := range keys {
		switch v := config[key].(type) {
		case map[string]interface{}:
			sections[key] = v
		case nil:
			m := map[string]interface{}{}
			config[key] = m
			sections[key] = m
		default:
			return nil, fmt.Errorf("public kubo config: %s is a %T, not an object; fix or remove it in the repo config and retry", key, v)
		}
	}
	return sections, nil
}

// setPublicSections sets the public node's listeners, filters, RPC bearer,
// providing, routing and storage limit.
func setPublicSections(sections map[string]map[string]interface{}, token string, declaredBytes uint64, apiHost string) {
	addresses := sections["Addresses"]
	addresses["API"] = []string{fmt.Sprintf("/ip4/%s/tcp/%d", apiHost, constants.GlobalIPFSAPIPort)}
	addresses["Gateway"] = []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", constants.GlobalIPFSGatewayPort)}
	addresses["Swarm"] = []string{
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", constants.GlobalIPFSSwarmPort),
		fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", constants.GlobalIPFSSwarmPort),
	}
	// The node announces what it listens on and nothing else: an address
	// announced by hand in an earlier config is dropped.
	addresses["Announce"] = []string{}
	addresses["AppendAnnounce"] = []string{}
	addresses["NoAnnounce"] = append([]string{}, publicSwarmFilters...)
	sections["Swarm"]["AddrFilters"] = append([]string{}, publicSwarmFilters...)
	sections["API"]["Authorizations"] = map[string]interface{}{
		ipfs.KuboAPIUser: map[string]interface{}{
			"AuthSecret":   "bearer:" + token,
			"AllowedPaths": append([]string{}, PublicAPIAllowedPaths...),
		},
	}
	sections["Provide"]["Strategy"] = "pinned"
	sections["Routing"]["Type"] = "dht"
	sections["Datastore"]["StorageMax"] = PublicStorageMax(declaredBytes)
}

// WritePublicKuboFiles writes config, the RPC token, and an empty denylist
// into dir. It refuses when dir already contains swarm.key. root is required
// because install runs as root and must not follow a symlink planted in the
// repo.
func WritePublicKuboFiles(root rootfs.Root, dir, token string, declaredBytes uint64, existing []byte, apiHost string) error {
	swarm := filepath.Join(dir, "swarm.key")
	if _, err := root.ReadFile(swarm, rootfs.SmallFileLimit); err == nil {
		return fmt.Errorf("public kubo repo %s already has a swarm.key; it would join a private swarm", dir)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read swarm.key: %w", err)
	}
	body, err := PublicKuboConfig(existing, token, declaredBytes, apiHost)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("mkdir public kubo repo: %w", err)
	}
	if err := root.WriteFile(filepath.Join(dir, "config"), body, 0600); err != nil {
		return fmt.Errorf("write public kubo config: %w", err)
	}
	if err := root.WriteFile(filepath.Join(dir, PublicAPITokenFile), []byte(token+"\n"), 0640); err != nil {
		return fmt.Errorf("write public kubo token: %w", err)
	}
	deny := filepath.Join(dir, PublicDenylistFile)
	if _, err := root.ReadFile(deny, rootfs.SmallFileLimit); errors.Is(err, fs.ErrNotExist) {
		if err := root.WriteFile(deny, nil, 0640); err != nil {
			return fmt.Errorf("write public kubo denylist: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("read public kubo denylist: %w", err)
	}
	return nil
}

// ParseDenylist reads one CID per line. Blank lines and lines starting with
// # are ignored. Anything else is refused, so a typo does not become a hole
// in the denylist.
func ParseDenylist(text string) ([]string, error) {
	var out []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsAny(line, " \t") || len(line) < 10 {
			return nil, fmt.Errorf("denylist line %d is not a CID", i+1)
		}
		out = append(out, line)
	}
	return out, nil
}
