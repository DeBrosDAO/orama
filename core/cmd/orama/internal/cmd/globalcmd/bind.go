package globalcmd

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/globalbind"
	"github.com/spf13/cobra"
)

// Cmd is the global role: installing and running the orama-global-* units on
// this node, the validator key, and the on-chain node messages.
var Cmd = &cobra.Command{
	Use:   "global",
	Short: "Install and operate a global node, and build its chain messages",
	Long: `Operate the global role.

On the node, as root: install puts the global services on this machine;
start, stop, restart and status run their units in order, chain first;
validator backs up and migrates the consensus key and builds unjail and edit
messages; stage-oramad places a verified chain binary for cosmovisor.

bind signs the binding that proves a service key belongs to an operator. The
private key stays in its file; the command writes the public key and the
signature. register, bond, unbond, capacity and retire build the node's chain
messages.`,
}

var bindFlags struct {
	chainID  string
	operator string
	service  string
	keyFile  string
	keyType  string
}

var bindCmd = &cobra.Command{
	Use:   "bind",
	Short: "Sign orama-global-bind-v1 for one service key",
	Args:  cobra.NoArgs,
	RunE:  runBind,
}

func init() {
	f := bindCmd.Flags()
	f.StringVar(&bindFlags.chainID, "chain-id", "", "Chain id the binding is for [required]")
	f.StringVar(&bindFlags.operator, "operator", "", "Operator account (orama1...) [required]")
	f.StringVar(&bindFlags.service, "service", "", "Service name, for example provider or tor [required]")
	f.StringVar(&bindFlags.keyFile, "key-file", "", "Service secret file [required]")
	f.StringVar(&bindFlags.keyType, "key-type", "", "secp256k1, ed25519, or ed25519-expanded; required for a raw 32-byte file")
	Cmd.AddCommand(bindCmd)
}

func runBind(cmd *cobra.Command, args []string) error {
	if bindFlags.chainID == "" || bindFlags.operator == "" || bindFlags.service == "" || bindFlags.keyFile == "" {
		return clierr.Usage("chain-id, operator, service, and key-file are required")
	}
	key, err := globalbind.ReadKeyFile(bindFlags.keyFile)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if bindFlags.keyType != "" {
		key.Kind = bindFlags.keyType
	}
	if key.Kind == globalbind.KeyTypeSecp256k1 && len(key.Secret) == 32 && bindFlags.keyType == "" && !cometJSON(bindFlags.keyFile) {
		// A raw 32-byte file is ambiguous (secp256k1 secret or ed25519 seed).
		// CometBFT JSON names its type. A raw file must say.
		return clierr.Usage("a 32-byte key file needs --key-type secp256k1 or ed25519")
	}
	binding, err := globalbind.SignKey(key, bindFlags.chainID, bindFlags.operator, bindFlags.service)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	out := map[string]string{
		"service":   binding.Service,
		"key_type":  binding.KeyType,
		"pubkey":    hex.EncodeToString(binding.Pubkey),
		"signature": hex.EncodeToString(binding.Signature),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return clierr.Failure("write the binding: %w", err)
	}
	fmt.Fprintln(os.Stderr, "binding signed; not submitted")
	return nil
}

func cometJSON(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [1]byte
	if _, err := f.Read(b[:]); err != nil {
		return false
	}
	return b[0] == '{'
}
