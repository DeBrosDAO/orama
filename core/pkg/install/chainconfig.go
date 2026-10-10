package install

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The settings every Orama chain node runs with, the ones chain/scripts/stagenet/
// deploy.sh used to write with sed. A multiple of the pruning interval makes a
// snapshot a height the store keeps whole, and two kept snapshots let a joiner
// that starts one while the next is cut still finish.
const (
	// chainPruningKeepRecent and chainPruningInterval: custom pruning keeps the
	// last 100 states and prunes every 10 blocks.
	chainPruningKeepRecent = "100"
	chainPruningInterval   = "10"
	// ChainSnapshotInterval is how often a node cuts a state-sync snapshot, in
	// blocks. It must be a multiple of the pruning interval.
	ChainSnapshotInterval = 1000
	// ChainSnapshotKeepRecent is how many snapshots a node keeps.
	ChainSnapshotKeepRecent = 2
	// chainMinRetainBlocks keeps 14 days of blocks at 6 seconds (x/archive
	// DefaultBlocksIn14Days): oramad's Commit never returns a retain height above
	// the last archived height, so this prunes nothing while no range is archived.
	chainMinRetainBlocks = 201600
	// chainIAVLCacheSize and chainQueryGasLimit are what `oramad init` writes
	// (chain/cmd/oramad/cmd defaultIAVLCacheSize, defaultQueryGasLimit); a home
	// whose app.toml predates them gets them here.
	chainIAVLCacheSize = 100000
	chainQueryGasLimit = 2000000
	chainAppDBBackend  = "pebbledb"
	// StateSyncTrustPeriod is how long a trusted header stays trusted. It must be
	// shorter than the unbonding period (21 days), or a long-range attacker's old
	// validators could still sign a header the joiner accepts.
	StateSyncTrustPeriod = 7 * 24 * time.Hour
	// stateSyncMinServers is how many independent light-client servers a joiner
	// needs: CometBFT cross-checks a header between them.
	stateSyncMinServers = 2
	// chainFileMode is config.toml and app.toml: the chain account's alone.
	chainFileMode = 0o600
)

// StateSyncJoin is a joiner's [statesync] block: the nodes it fetches a snapshot
// and verifies it through, and the header it starts trusting from.
type StateSyncJoin struct {
	// RPCServers are the light-client endpoints (https://<seed>/v1/chain/light),
	// at least two.
	RPCServers []string
	// TrustHeight and TrustHash are a block every one of the servers agreed on.
	TrustHeight int64
	TrustHash   string
}

// ChainConfig is what the installer writes into the chain's config.toml and
// app.toml.
type ChainConfig struct {
	// ExternalAddress is the host:port the node announces to its peers: its
	// public address and the p2p port. The chain listens at the netns address
	// (198.18.0.2), which no peer can reach, so without it a peer is told that.
	ExternalAddress string
	// StateSync, when set, makes the node restore a snapshot instead of
	// replaying the chain from its genesis.
	StateSync *StateSyncJoin
}

// Validate checks the config before anything on the host changes.
func (c ChainConfig) Validate() error {
	host, port, err := net.SplitHostPort(c.ExternalAddress)
	if err != nil {
		return fmt.Errorf("chain external address %q is not host:port: %w", c.ExternalAddress, err)
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("chain external address %q: the host must be the node's public IP address", c.ExternalAddress)
	}
	if p, err := strconv.Atoi(port); err != nil || p != constants.ChainP2PPort {
		return fmt.Errorf("chain external address %q: the port must be the chain's p2p port %d", c.ExternalAddress, constants.ChainP2PPort)
	}
	if c.StateSync != nil {
		return c.StateSync.validate()
	}
	return nil
}

func (s StateSyncJoin) validate() error {
	if len(s.RPCServers) < stateSyncMinServers {
		return fmt.Errorf("state sync needs %d independent light-client servers, got %d", stateSyncMinServers, len(s.RPCServers))
	}
	seen := map[string]bool{}
	for _, raw := range s.RPCServers {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || strings.ContainsAny(raw, `",`) {
			return fmt.Errorf("state-sync server %q is not an https:// URL", raw)
		}
		if seen[u.Host] {
			return fmt.Errorf("state-sync server %q is listed twice: the servers must be independent", u.Host)
		}
		seen[u.Host] = true
	}
	if s.TrustHeight < 1 {
		return fmt.Errorf("state-sync trust height %d must be positive", s.TrustHeight)
	}
	if h, err := hex.DecodeString(s.TrustHash); err != nil || len(h) != 32 {
		return fmt.Errorf("state-sync trust hash %q is not a 32-byte hex hash", s.TrustHash)
	}
	return nil
}

// RenderChainConfig applies c to the two files' contents and returns the new
// ones. It is a pure function of its input, and writing the result back again
// changes nothing.
func RenderChainConfig(c ChainConfig, configToml, appToml string) (config, app string, err error) {
	if err := c.Validate(); err != nil {
		return "", "", err
	}
	config, err = editAll(configToml, c.configEdits())
	if err != nil {
		return "", "", fmt.Errorf("config.toml: %w", err)
	}
	app, err = editAll(appToml, chainAppEdits())
	if err != nil {
		return "", "", fmt.Errorf("app.toml: %w", err)
	}
	return config, app, nil
}

type tomlEdit struct{ section, key, value string }

func (c ChainConfig) configEdits() []tomlEdit {
	edits := []tomlEdit{
		{"p2p", "external_address", quote(c.ExternalAddress)},
		{"p2p", "pex", "false"},
		{"p2p", "addr_book_strict", "false"},
		{"instrumentation", "prometheus", "true"},
		{"instrumentation", "prometheus_listen_addr", quote("127.0.0.1:" + strconv.Itoa(constants.ChainPrometheusPort))},
	}
	if s := c.StateSync; s != nil {
		edits = append(edits,
			tomlEdit{"statesync", "enable", "true"},
			tomlEdit{"statesync", "rpc_servers", quote(strings.Join(s.RPCServers, ","))},
			tomlEdit{"statesync", "trust_height", strconv.FormatInt(s.TrustHeight, 10)},
			tomlEdit{"statesync", "trust_hash", quote(strings.ToLower(s.TrustHash))},
			tomlEdit{"statesync", "trust_period", quote(StateSyncTrustPeriod.String())},
		)
	}
	return edits
}

func chainAppEdits() []tomlEdit {
	return []tomlEdit{
		{"", "pruning", quote("custom")},
		{"", "pruning-keep-recent", quote(chainPruningKeepRecent)},
		{"", "pruning-interval", quote(chainPruningInterval)},
		{"", "min-retain-blocks", strconv.Itoa(chainMinRetainBlocks)},
		{"", "app-db-backend", quote(chainAppDBBackend)},
		{"", "iavl-cache-size", strconv.Itoa(chainIAVLCacheSize)},
		{"", "query-gas-limit", quote(strconv.Itoa(chainQueryGasLimit))},
		{"state-sync", "snapshot-interval", strconv.Itoa(ChainSnapshotInterval)},
		{"state-sync", "snapshot-keep-recent", strconv.Itoa(ChainSnapshotKeepRecent)},
	}
}

func editAll(doc string, edits []tomlEdit) (string, error) {
	var err error
	for _, e := range edits {
		if doc, err = tomlSet(doc, e.section, e.key, e.value); err != nil {
			return "", fmt.Errorf("set %s: %w", e.key, err)
		}
	}
	return doc, nil
}

func quote(s string) string { return strconv.Quote(s) }

// applyChainConfig reads the chain home's two files, renders c into them and
// writes them back, owned by the chain account. The home must exist: it is made
// by --init-chain or by an earlier install.
func applyChainConfig(h GlobalHost, c ChainConfig) error {
	dir := filepath.Join(h.ChainHome, "config")
	uid, gid, err := h.Lookup(constants.ChainUser)
	if err != nil {
		return err
	}
	read := func(name string) (string, string, error) {
		path := filepath.Join(dir, name)
		data, err := h.StateRoot.ReadFile(path, chainConfigLimit)
		if err != nil {
			return "", "", fmt.Errorf("read %s (is the chain home initialised? pass --init-chain on the first install): %w", path, err)
		}
		return path, string(data), nil
	}
	configPath, configToml, err := read("config.toml")
	if err != nil {
		return err
	}
	appPath, appToml, err := read("app.toml")
	if err != nil {
		return err
	}
	config, app, err := RenderChainConfig(c, configToml, appToml)
	if err != nil {
		return fmt.Errorf("chain home %s: %w", h.ChainHome, err)
	}
	for path, body := range map[string]string{configPath: config, appPath: app} {
		if err := writeChainFile(h, path, []byte(body), uid, gid); err != nil {
			return err
		}
	}
	h.Logf("  ✓ chain config written (external address %s, pruning, snapshots%s)", c.ExternalAddress, stateSyncNote(c))
	return nil
}

// chainConfigLimit bounds a config file read: a real one is under 30 KB.
const chainConfigLimit = 1 << 20

func stateSyncNote(c ChainConfig) string {
	if c.StateSync == nil {
		return ""
	}
	return fmt.Sprintf(", state sync from height %d", c.StateSync.TrustHeight)
}

func writeChainFile(h GlobalHost, path string, body []byte, uid, gid int) error {
	if err := h.StateRoot.WriteFile(path, body, chainFileMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := h.Chown(h.StateRoot, path, uid, gid); err != nil {
		return fmt.Errorf("chown %s to %s: %w", path, constants.ChainUser, err)
	}
	return nil
}
