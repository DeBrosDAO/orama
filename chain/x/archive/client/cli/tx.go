package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// GetTxCmd returns the parent `archive` transaction command.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Archive transaction subcommands",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		GetCmdAttest(),
		GetCmdAttachReplicas(),
	)
	return cmd
}

// GetCmdAttest implements `oramad tx archive attest`. The signer is --from, written into the archiver field.
func GetCmdAttest() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attest [start-height] [end-height] [bundle-cid] [bundle-hash-hex] [merkle-root-hex]",
		Short: "Attest a height range's bundle CID, content hash and block-hash Merkle root",
		Args:  cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			start, err := parseHeight(args[0])
			if err != nil {
				return err
			}
			end, err := parseHeight(args[1])
			if err != nil {
				return err
			}
			bundleHash, err := decodeHash(args[3])
			if err != nil {
				return fmt.Errorf("bundle hash: %w", err)
			}
			root, err := decodeHash(args[4])
			if err != nil {
				return fmt.Errorf("merkle root: %w", err)
			}
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			nodeID, err := cmd.Flags().GetString(flagNodeID)
			if err != nil {
				return fmt.Errorf("read --%s: %w", flagNodeID, err)
			}
			msg := &types.MsgAttest{
				Archiver:    clientCtx.GetFromAddress().String(),
				NodeId:      nodeID,
				StartHeight: start,
				EndHeight:   end,
				BundleCid:   args[2],
				BundleHash:  bundleHash,
				MerkleRoot:  root,
			}
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	addNodeIDFlag(cmd)
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// GetCmdAttachReplicas implements `oramad tx archive attach-replicas`. The signer is the archiver.
func GetCmdAttachReplicas() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach-replicas [start-height] [end-height] [deal-id...]",
		Short: "Record replica deal ids for an attested height range",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			start, err := parseHeight(args[0])
			if err != nil {
				return err
			}
			end, err := parseHeight(args[1])
			if err != nil {
				return err
			}
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return fmt.Errorf("failed to get client context: %w", err)
			}
			nodeID, err := cmd.Flags().GetString(flagNodeID)
			if err != nil {
				return fmt.Errorf("read --%s: %w", flagNodeID, err)
			}
			msg := &types.MsgAttachReplicas{
				Archiver:    clientCtx.GetFromAddress().String(),
				NodeId:      nodeID,
				StartHeight: start,
				EndHeight:   end,
				DealIds:     append([]string(nil), args[2:]...),
			}
			if err := msg.ValidateBasic(); err != nil {
				return err
			}
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	addNodeIDFlag(cmd)
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// flagNodeID names the archiver node the signer's hot key belongs to. x/archive
// counts one attestation per operator, keyed through that node.
const flagNodeID = "node-id"

func addNodeIDFlag(cmd *cobra.Command) {
	cmd.Flags().String(flagNodeID, "", "x/nodes id of the ARCHIVER node whose hot key signs (required)")
	_ = cmd.MarkFlagRequired(flagNodeID)
}

func parseHeight(s string) (int64, error) {
	height, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid height %q: %w", s, err)
	}
	return height, nil
}
