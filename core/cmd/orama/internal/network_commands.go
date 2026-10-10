package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

// A network is a name the CLI knows. It can be a network of the registry (a
// manifest: chain id, seeds, release root), a cluster the operator reaches
// through a gateway (an environment in environments.json), or both under one
// name. `orama network` lists and chooses among them; the gateway and the
// credentials are chosen by the same name as before `orama env` became it.

const (
	// networkStoreDirName is the directory of ~/.orama that holds the networks
	// added by URL.
	networkStoreDirName = "networks"

	// sourceBuiltin, sourceAdded and sourceConfigured say where a row of the
	// network list comes from.
	sourceBuiltin    = "built in"
	sourceAdded      = "added"
	sourceConfigured = "configured"

	// noValue stands for a cell with nothing in it.
	noValue = "-"
)

// networkStoreDirFn and embeddedNetworksFn are overridden by tests.
var (
	networkStoreDirFn  = defaultNetworkStoreDir
	embeddedNetworksFn = netregistry.Embedded
)

func defaultNetworkStoreDir() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}
	return filepath.Join(dir, networkStoreDirName), nil
}

func networkStore() (netregistry.Store, error) {
	dir, err := networkStoreDirFn()
	if err != nil {
		return netregistry.Store{}, err
	}
	return netregistry.Store{Dir: dir}, nil
}

// LoadNetworks returns the networks built into this binary and the ones the
// operator added.
func LoadNetworks() (*netregistry.Registry, error) {
	builtin, err := embeddedNetworksFn()
	if err != nil {
		return nil, err
	}
	store, err := networkStore()
	if err != nil {
		return nil, err
	}
	added, err := store.Load()
	if err != nil {
		return nil, err
	}
	all, err := builtin.Merge(added)
	if err != nil {
		return nil, fmt.Errorf("%w: a network you added has the name of a built-in one; drop yours with `orama network remove <name>`", err)
	}
	return all, nil
}

// NetworkList prints every network the CLI knows, one row each: the registry's,
// the gateways the operator configured, and the names that are both.
func NetworkList(p *printer.Printer) error {
	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize networks: %w", err)
	}
	cfg, err := LoadEnvironmentConfig()
	if err != nil {
		return clierr.Failure("failed to load network config: %w", err)
	}
	registry, err := LoadNetworks()
	if err != nil {
		return clierr.Failure("failed to load the network registry: %w", err)
	}
	return p.Table([]string{"NAME", "CHAIN", "GATEWAY", "ACTIVE", "SOURCE", "DESCRIPTION"}, networkRows(cfg, registry))
}

// networkRows is the union of the configured gateways and the registry, sorted
// by name.
func networkRows(cfg *EnvironmentConfig, registry *netregistry.Registry) [][]string {
	rows := map[string][]string{}
	for _, name := range registry.Names() {
		n, _ := registry.Get(name)
		source := sourceAdded
		if n.Builtin {
			source = sourceBuiltin
		}
		rows[name] = []string{name, n.Manifest.ChainID, noValue, "", source, noValue}
	}
	for _, env := range cfg.Environments {
		row, known := rows[env.Name]
		if !known {
			row = []string{env.Name, chainOf(env, registry), noValue, "", sourceConfigured, noValue}
		} else {
			row[4] += ", " + sourceConfigured
		}
		row[2] = env.GatewayURL
		if env.Name == cfg.ActiveEnvironment {
			row[3] = "*"
		}
		if env.Description != "" {
			row[5] = env.Description
		}
		rows[env.Name] = row
	}
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([][]string, 0, len(names))
	for _, name := range names {
		out = append(out, rows[name])
	}
	return out
}

// chainOf is the chain id of the registry network a configured gateway runs
// on, or noValue when it names none.
func chainOf(env Environment, registry *netregistry.Registry) string {
	if env.Network == "" {
		return noValue
	}
	if n, err := registry.Get(env.Network); err == nil {
		return n.Manifest.ChainID
	}
	return noValue
}

// NetworkCurrent prints the active network and its gateway URL.
func NetworkCurrent(p *printer.Printer) error {
	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize networks: %w", err)
	}
	env, err := GetActiveEnvironment()
	if err != nil {
		return clierr.NotFound("no active network: %w", err)
	}
	if p.JSONMode() {
		return p.JSON(env)
	}
	p.Printf("Current network: %s\n", env.Name)
	p.Printf("   Gateway URL: %s\n", env.GatewayURL)
	if env.Network != "" {
		p.Printf("   Registry network: %s\n", env.Network)
	}
	p.Printf("   Description: %s\n", env.Description)
	return nil
}

// NetworkUse makes the named network active: a configured gateway of that name,
// or else the one gateway configured on the registry network of that name.
func NetworkUse(p *printer.Printer, name string) error {
	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize networks: %w", err)
	}
	cfg, err := LoadEnvironmentConfig()
	if err != nil {
		return clierr.Failure("failed to load network config: %w", err)
	}
	target, err := resolveUse(cfg, name)
	if err != nil {
		return err
	}
	old, _ := GetActiveEnvironment()
	if err := SwitchEnvironment(target); err != nil {
		return clierr.NotFound("failed to switch to %q: %w", target, err)
	}
	now, err := GetActiveEnvironment()
	if err != nil {
		return clierr.Failure("switched, but could not read the new network back: %w", err)
	}
	if old != nil && old.Name != now.Name {
		p.Printf("Switched network: %s -> %s\n", old.Name, now.Name)
	} else {
		p.Printf("Network set to: %s\n", now.Name)
	}
	p.Printf("   Gateway URL: %s\n", now.GatewayURL)
	return nil
}

// resolveUse names the configured gateway `network use name` selects.
func resolveUse(cfg *EnvironmentConfig, name string) (string, error) {
	var onNetwork []string
	for _, env := range cfg.Environments {
		if env.Name == name {
			return name, nil
		}
		if env.Network == name {
			onNetwork = append(onNetwork, env.Name)
		}
	}
	switch len(onNetwork) {
	case 1:
		return onNetwork[0], nil
	case 0:
		return "", clierr.NotFound("network %q not found: no gateway is called %q and none runs on a network of that name; "+
			"add one with `orama network add %s https://<gateway> --network %s`", name, name, name, name)
	}
	sort.Strings(onNetwork)
	return "", clierr.Usage("%d gateways run on network %q (%s): choose one with `orama network use <name>`", len(onNetwork), name, strings.Join(onNetwork, ", "))
}

// validateNewEnvironment refuses a gateway no command could use: a blank name,
// or a gateway URL without an http(s) scheme and a host. They were stored as
// given and every later command failed on them instead.
func validateNewEnvironment(name, gatewayURL string) error {
	if strings.TrimSpace(name) == "" {
		return clierr.Usage("a network needs a name")
	}
	u, err := url.Parse(gatewayURL)
	if err != nil || u.Host == "" {
		return clierr.Usage("gateway URL %q is not a URL: give it as https://<host>", gatewayURL)
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
		return nil
	}
	return clierr.Usage("gateway URL %q must be https:// (http:// only for a gateway on this machine): "+
		"every command sends its credential there", gatewayURL)
}

// isLoopbackHost reports whether host is this machine: localhost, a
// *.localhost name, or a loopback address.
func isLoopbackHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ClusterOptions are the optional parts of adding a gateway.
type ClusterOptions struct {
	// CAFile is a PEM bundle trusted for the gateway's domain only.
	CAFile string
	// Network is the registry network the gateway runs on.
	Network string
}

// NetworkAddCluster registers a gateway under a name. args are the name, the
// gateway URL and an optional description; cobra guarantees the count. A
// registry network named in opts must exist.
func NetworkAddCluster(p *printer.Printer, args []string, opts ClusterOptions) error {
	name, gatewayURL, description := args[0], args[1], ""
	if len(args) > 2 {
		description = args[2]
	}
	if err := validateNewEnvironment(name, gatewayURL); err != nil {
		return err
	}
	if opts.Network != "" {
		registry, err := LoadNetworks()
		if err != nil {
			return clierr.Failure("failed to load the network registry: %w", err)
		}
		if _, err := registry.Get(opts.Network); err != nil {
			return clierr.NotFound("%v", err)
		}
	}
	if err := InitializeEnvironments(); err != nil {
		return clierr.Failure("failed to initialize networks: %w", err)
	}
	if err := AddEnvironmentOn(name, gatewayURL, description, opts.Network); err != nil {
		return clierr.Failure("failed to add network %q: %w", name, err)
	}
	if opts.CAFile != "" {
		if err := SetEnvironmentCA(name, opts.CAFile); err != nil {
			return clierr.Failure("added %q, but its CA file was refused: %w", name, err)
		}
	}
	p.Printf("Added network: %s\n", name)
	p.Printf("   Gateway URL: %s\n", gatewayURL)
	if description != "" {
		p.Printf("   Description: %s\n", description)
	}
	if opts.Network != "" {
		p.Printf("   Registry network: %s\n", opts.Network)
	}
	if opts.CAFile != "" {
		p.Printf("   Trusted CA:  %s (for this network's domain only)\n", opts.CAFile)
	}
	return nil
}

// NetworkAddManifest fetches the network whose manifest is at rawURL, shows what
// the operator would trust (the chain id and the digest of the release root),
// and stores it once they confirm on in. yes confirms without asking, for
// scripts.
func NetworkAddManifest(ctx context.Context, p *printer.Printer, client *http.Client, in io.Reader, rawURL string, yes bool) error {
	n, err := netregistry.FetchNetwork(ctx, client, rawURL)
	if err != nil {
		return clierr.Failure("fetch the network manifest: %w", err)
	}
	m := n.Manifest
	builtin, err := embeddedNetworksFn()
	if err != nil {
		return clierr.Failure("failed to load the network registry: %w", err)
	}
	if _, err := builtin.Get(m.Name); err == nil {
		return clierr.Usage("network %q is built into this binary; a network added by URL cannot take its name", m.Name)
	}
	store, err := networkStore()
	if err != nil {
		return clierr.Failure("%w", err)
	}
	p.Printf("Network:       %s\n", m.Name)
	p.Printf("Chain id:      %s\n", m.ChainID)
	p.Printf("Release root:  sha256 %s\n", m.ReleaseRootSHA256)
	p.Printf("Release repo:  %s (channel %s)\n", m.ReleaseRepo, m.Channel)
	p.Printf("Source:        %s\n", rawURL)
	if replaced := replacedBy(store, m.Name); replaced != "" {
		p.Printf("Replaces:      %s\n", replaced)
	}
	p.Printf("\nEverything this network's nodes install is verified against that release root.\n")
	if !yes {
		p.Printf("Trust it? Type yes to continue: ")
		if err := clierr.Confirm(in, "yes"); err != nil {
			return err
		}
	}
	if err := store.Save(n); err != nil {
		return clierr.Failure("%w", err)
	}
	p.Printf("Added network: %s (chain %s)\n", m.Name, m.ChainID)
	return nil
}

// replacedBy describes the stored network a new one of the same name replaces,
// or "" when there is none.
func replacedBy(store netregistry.Store, name string) string {
	stored, err := store.Load()
	if err != nil {
		return ""
	}
	old, err := stored.Get(name)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("chain %s, release root sha256 %s", old.Manifest.ChainID, old.Manifest.ReleaseRootSHA256)
}

// NetworkRemove forgets a network: the added network and the configured gateway
// of that name, whichever there are. A built-in network cannot be removed. A
// name that is neither is not an error.
func NetworkRemove(p *printer.Printer, name string) error {
	builtin, err := embeddedNetworksFn()
	if err != nil {
		return clierr.Failure("failed to load the network registry: %w", err)
	}
	_, builtinErr := builtin.Get(name)
	store, err := networkStore()
	if err != nil {
		return clierr.Failure("%w", err)
	}
	if _, err := store.Remove(name); err != nil {
		return clierr.Failure("%w", err)
	}
	if err := RemoveEnvironment(name); err != nil {
		return clierr.NotFound("failed to remove network %q: %w", name, err)
	}
	p.Printf("Removed network: %s\n", name)
	if builtinErr == nil {
		p.Printf("   %s is built into this binary and stays in the list; its gateway entry is gone\n", name)
	}
	return nil
}
