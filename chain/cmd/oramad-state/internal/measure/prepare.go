package measure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// DefaultChainID names a localnet chain: x/power's bootstrap-committee gate allows a committee
	// of one only on a localnet chain id (params.ClassOfChainID: orama-[<name>-]localnet-<id>).
	DefaultChainID = "orama-localnet-bench-1"
	denom          = "norama"
	keyName        = "validator"
	keyringBackend = "test"
	moniker        = "bench"
	// epochDuration and minBlocksPerEpoch are the localnet's shortened epoch, as scripts/localnet.
	epochDuration     = "30s"
	minBlocksPerEpoch = "5"
	// blockMaxGas is the consensus block gas limit a localnet is given.
	blockMaxGas = "100000000"
)

// Prepare makes a node home for a chain of one validator: `oramad init`, a key, the bootstrap
// committee of one, the emission and consensus settings of the localnet script, and a config.toml
// that binds nothing outside the machine and has no profiler port. The home holds no blocks; a state
// generator or the first start fills it. timeoutCommit, when set (e.g. "50ms"), replaces CometBFT's
// wait between blocks.
func Prepare(ctx context.Context, o Oramad, home, chainID, timeoutCommit string) error {
	if err := o.CheckBinary(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(home, "config")); err == nil {
		return fmt.Errorf("%s is already a node home; prepare a new directory", home)
	}
	steps := [][]string{
		{"init", moniker, "--chain-id", chainID, "--default-denom", denom, "--home", home},
		{"keys", "add", keyName, "--keyring-backend", keyringBackend, "--home", home},
	}
	for _, args := range steps {
		if _, err := o.Run(ctx, args...); err != nil {
			return err
		}
	}
	addr, err := o.Run(ctx, "keys", "show", keyName, "-a", "--keyring-backend", keyringBackend, "--home", home)
	if err != nil {
		return err
	}
	genesisSteps := [][]string{
		{"genesis", "set-emission-params", "--epoch-duration", epochDuration, "--min-blocks-per-epoch", minBlocksPerEpoch, "--allow-bootstrap-stake", "--home", home},
		{"genesis", "add-bootstrap-validator", strings.TrimSpace(addr.Stdout), "--moniker", moniker, "--home", home,
			"--consensus-pubkey-file", filepath.Join(home, "config", "priv_validator_key.json"), "--min-committee-size", "1"},
	}
	for _, args := range genesisSteps {
		if _, err := o.Run(ctx, args...); err != nil {
			return err
		}
	}
	if err := setConsensusParams(filepath.Join(home, "config", "genesis.json")); err != nil {
		return err
	}
	if _, err := o.Run(ctx, "genesis", "validate", "--home", home); err != nil {
		return err
	}
	return patchConfig(filepath.Join(home, "config", "config.toml"), timeoutCommit)
}

// setConsensusParams gives the genesis a finite block gas limit, which oramad's module wiring cannot
// default, and no vote extensions: the settings scripts/localnet/localnet.sh patches in.
func setConsensusParams(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}
	consensus := child(doc, "consensus")
	params := child(consensus, "params")
	child(params, "block")["max_gas"] = blockMaxGas
	child(params, "abci")["vote_extensions_enable_height"] = "0"
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0o600)
}

// child is m[key] as an object, created when it is absent.
func child(m map[string]any, key string) map[string]any {
	if sub, ok := m[key].(map[string]any); ok {
		return sub
	}
	sub := map[string]any{}
	m[key] = sub
	return sub
}

var (
	pprofLine         = regexp.MustCompile(`(?m)^pprof_laddr = .*$`)
	addrBookLine      = regexp.MustCompile(`(?m)^addr_book_strict = true$`)
	duplicateIPLine   = regexp.MustCompile(`(?m)^allow_duplicate_ip = false$`)
	timeoutCommitLine = regexp.MustCompile(`(?m)^timeout_commit = .*$`)
)

// patchConfig turns off the profiler port, lets the node run beside others on one machine and sets the
// block wait.
func patchConfig(path, timeoutCommit string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	text := pprofLine.ReplaceAllString(string(raw), `pprof_laddr = ""`)
	text = addrBookLine.ReplaceAllString(text, "addr_book_strict = false")
	text = duplicateIPLine.ReplaceAllString(text, "allow_duplicate_ip = true")
	if timeoutCommit != "" {
		text = timeoutCommitLine.ReplaceAllString(text, `timeout_commit = "`+timeoutCommit+`"`)
	}
	return os.WriteFile(path, []byte(text), 0o600)
}
