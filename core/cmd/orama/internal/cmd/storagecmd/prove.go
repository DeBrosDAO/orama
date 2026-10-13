package storagecmd

import (
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmd/globalcmd"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/spf13/cobra"
)

var proveFlags struct {
	chainID  string
	signer   string
	nodeID   string
	file     string
	pubKey   string
	account  uint64
	sequence uint64
	fee      string
	gas      uint64
	node     string
}

func init() {
	prove := &cobra.Command{
		Use:   "prove",
		Short: "Submit storage challenge proofs",
		Long: `Submit one or more challenge proofs for a storage node.

--file is a JSON array. Each object has deal_id, slot, leaf_index, leaf,
and siblings. leaf and siblings are hex. The leaf is 1024 bytes and each
sibling is 32 bytes. The command does not choose the challenged leaf and
does not read the stored piece. Without --node it prints the sign document
and does not submit it.`,
		Args: cobra.NoArgs,
		RunE: runProve,
	}
	f := prove.Flags()
	f.StringVar(&proveFlags.chainID, "chain-id", "", "Chain id [required]")
	f.StringVar(&proveFlags.signer, "signer", "", "Hot key account (orama1...) [required]")
	f.StringVar(&proveFlags.nodeID, "id", "", "Node id [required]")
	f.StringVar(&proveFlags.file, "file", "", "JSON file of proofs [required]")
	f.StringVar(&proveFlags.pubKey, "pubkey", "", "Compressed secp256k1 pubkey hex of the signing account")
	f.Uint64Var(&proveFlags.account, "account-number", 0, "Account number, when not read from --node")
	f.Uint64Var(&proveFlags.sequence, "sequence", 0, "Account sequence, when not read from --node")
	f.StringVar(&proveFlags.fee, "fee", "", "Fee in norama [required]")
	f.Uint64Var(&proveFlags.gas, "gas", 0, "Gas limit [required]")
	f.StringVar(&proveFlags.node, "node", "", "Chain REST API, for example http://127.0.0.1:31003; the command returns once the transaction is in a block, and fails if the block refuses it")
	globalcmd.AddOnionFlags(f)
	Cmd.AddCommand(prove)
}

func runProve(cmd *cobra.Command, args []string) error {
	if proveFlags.file == "" {
		return clierr.Usage("file is required")
	}
	raw, err := os.ReadFile(proveFlags.file)
	if err != nil {
		return clierr.Failure("read the proof file: %w", err)
	}
	proofs, err := clusterreg.ParseProofs(raw)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	msg := clusterreg.Proofs{Signer: proveFlags.signer, NodeID: proveFlags.nodeID, Proofs: proofs}
	if err := clusterreg.ValidateProofs(msg); err != nil {
		return clierr.Usage("%v", err)
	}
	in := clusterreg.Direct{
		TypeURL: clusterreg.SubmitProofsTypeURL, Msg: clusterreg.EncodeProofs(msg),
		FeeAmount: proveFlags.fee, Gas: proveFlags.gas, ChainID: proveFlags.chainID,
		AccountNumber: proveFlags.account, Sequence: proveFlags.sequence,
	}
	return globalcmd.SubmitDirect(cmd, proveFlags.signer, proveFlags.node, proveFlags.pubKey, proveFlags.account, proveFlags.sequence, in, "proved "+proveFlags.nodeID)
}
