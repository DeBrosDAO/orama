package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

const (
	flagMoniker               = "moniker"
	flagConsensusPubkeyFile   = "consensus-pubkey-file"
	flagConsensusPubkeyBase64 = "consensus-pubkey-base64"
	flagMinCommitteeSize      = "min-committee-size"
)

// privValidatorKeyFile is the shape of CometBFT's priv_validator_key.json this command reads the
// node's own consensus pubkey from - only the fields it needs, never the private key material.
type privValidatorKeyFile struct {
	PubKey struct {
		Value string `json:"value"`
	} `json:"pub_key"`
}

// AddBootstrapValidatorCmd returns the `genesis add-bootstrap-validator` command. It appends one
// BootstrapMember to x/power's genesis state in genesis.json, reading the node's own consensus
// pubkey directly from its priv_validator_key.json (written by `oramad init`) rather than asking
// the operator to paste it by hand.
//
// Like x/emission's set-emission-params, this only makes sense before the chain's first `oramad
// start`: x/power ships no Msg service, so the bootstrap committee is fixed at genesis
// (plans/open-network.md D18).
func AddBootstrapValidatorCmd(defaultNodeHome string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-bootstrap-validator [operator-address]",
		Short: "Add a bootstrap committee member to x/power's genesis state",
		Long: `Append one bootstrap committee member to x/power's genesis bootstrap_committee list in
genesis.json under --home. operator-address is the bech32 ACCOUNT address (not a valoper address)
that will own this seat's equal bootstrap share and earnings - typically that member's own
"validator" keyring key.

The member's consensus pubkey comes from one of three sources, in this order: --consensus-pubkey-base64
(the raw base64 ed25519 key, e.g. extracted remotely without ever transferring a private key file);
otherwise --consensus-pubkey-file (a priv_validator_key.json to read it from - only the public key
is ever read); otherwise --home's own priv_validator_key.json. This lets one node's genesis.json
collect every committee member's pubkey without copying private key material between node homes -
the same role 'genesis collect-gentxs' plays for a gentx-based genesis.

Must be run once per genesis validator, before the chain's first 'oramad start' and before
'genesis collect-gentxs' (there are no gentxs in a bootstrap-committee genesis: committee members
need no self-bond - see docs/CHAIN.md).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			operatorAddr := args[0]
			if _, err := sdk.AccAddressFromBech32(operatorAddr); err != nil {
				return fmt.Errorf("invalid operator-address %q: %w", operatorAddr, err)
			}
			moniker, err := cmd.Flags().GetString(flagMoniker)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flagMoniker, err)
			}
			pubkeyFile, err := cmd.Flags().GetString(flagConsensusPubkeyFile)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flagConsensusPubkeyFile, err)
			}
			pubkeyBase64, err := cmd.Flags().GetString(flagConsensusPubkeyBase64)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flagConsensusPubkeyBase64, err)
			}
			minCommitteeSize, err := cmd.Flags().GetUint64(flagMinCommitteeSize)
			if err != nil {
				return fmt.Errorf("failed to read --%s: %w", flagMinCommitteeSize, err)
			}

			clientCtx := client.GetClientContextFromCmd(cmd)
			serverCtx := server.GetServerContextFromCmd(cmd)
			cometConfig := serverCtx.Config
			cometConfig.SetRoot(clientCtx.HomeDir)

			var pubKeyBytes []byte
			switch {
			case pubkeyBase64 != "":
				pubKeyBytes, err = decodeConsensusPubKey(pubkeyBase64)
			case pubkeyFile != "":
				pubKeyBytes, err = readConsensusPubKeyFile(pubkeyFile)
			default:
				pubKeyBytes, err = readConsensusPubKeyFile(cometConfig.PrivValidatorKeyFile())
			}
			if err != nil {
				return err
			}

			genFile := cometConfig.GenesisFile()
			appGenesis, err := genutiltypes.AppGenesisFromFile(genFile)
			if err != nil {
				return fmt.Errorf("failed to read genesis file %s: %w", genFile, err)
			}

			var appState map[string]json.RawMessage
			if err := json.Unmarshal(appGenesis.AppState, &appState); err != nil {
				return fmt.Errorf("failed to unmarshal app state from %s: %w", genFile, err)
			}

			genState := *types.DefaultGenesisState()
			if raw, ok := appState[types.ModuleName]; ok && len(raw) > 0 {
				if err := clientCtx.Codec.UnmarshalJSON(raw, &genState); err != nil {
					return fmt.Errorf("failed to unmarshal existing %s genesis state: %w", types.ModuleName, err)
				}
			}
			if minCommitteeSize > 0 {
				genState.Params.MinCommitteeSize = minCommitteeSize
			}

			for _, m := range genState.BootstrapCommittee {
				if m.OperatorAddress == operatorAddr {
					return fmt.Errorf("operator_address %q is already a bootstrap committee member", operatorAddr)
				}
			}
			genState.BootstrapCommittee = append(genState.BootstrapCommittee, types.BootstrapMember{
				OperatorAddress: operatorAddr,
				Moniker:         moniker,
				ConsensusPubkey: pubKeyBytes,
			})

			appState[types.ModuleName] = clientCtx.Codec.MustMarshalJSON(&genState)
			rawAppState, err := json.Marshal(appState)
			if err != nil {
				return fmt.Errorf("failed to marshal app state: %w", err)
			}
			appGenesis.AppState = rawAppState

			if err := appGenesis.SaveAs(genFile); err != nil {
				return fmt.Errorf("failed to save genesis file %s: %w", genFile, err)
			}

			cmd.Printf("bootstrap committee member added: operator_address=%s moniker=%q (committee size now %d)\n",
				operatorAddr, moniker, len(genState.BootstrapCommittee))
			return nil
		},
	}

	cmd.Flags().String(flagMoniker, "", "human-readable label for this committee seat")
	cmd.Flags().String(flagConsensusPubkeyBase64, "", "raw base64 ed25519 consensus pubkey (takes priority over --consensus-pubkey-file)")
	cmd.Flags().String(flagConsensusPubkeyFile, "", "priv_validator_key.json to read the consensus pubkey from (default: --home's own)")
	cmd.Flags().Uint64(flagMinCommitteeSize, 0, "if set (>0), overwrite params.min_committee_size (devnet/stagenet/localnet chain-ids only need to satisfy this, not the 30-member production floor)")
	cmd.Flags().String(flags.FlagHome, defaultNodeHome, "The application home directory")

	return cmd
}

// readConsensusPubKeyFile reads and base64-decodes the ed25519 public key from a
// priv_validator_key.json file, without ever touching its private key material.
func readConsensusPubKeyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read priv validator key file %s: %w", path, err)
	}
	var keyFile privValidatorKeyFile
	if err := json.Unmarshal(raw, &keyFile); err != nil {
		return nil, fmt.Errorf("failed to parse priv validator key file %s: %w", path, err)
	}
	pubKey, err := decodeConsensusPubKey(keyFile.PubKey.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid consensus pubkey in %s: %w", path, err)
	}
	return pubKey, nil
}

// decodeConsensusPubKey base64-decodes a raw ed25519 consensus pubkey and checks its length.
func decodeConsensusPubKey(b64 string) ([]byte, error) {
	pubKey, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 consensus pubkey: %w", err)
	}
	if len(pubKey) != types.Ed25519PubKeyLen {
		return nil, fmt.Errorf("consensus pubkey is %d bytes, want exactly %d (ed25519)", len(pubKey), types.Ed25519PubKeyLen)
	}
	return pubKey, nil
}
