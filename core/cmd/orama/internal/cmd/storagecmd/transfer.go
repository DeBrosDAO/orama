package storagecmd

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/storageclient"
	"github.com/spf13/cobra"
)

func init() {
	put := &cobra.Command{
		Use:   "put",
		Short: "Upload sealed slots to the providers a deal assigned",
		Long: `Upload the slot-N files written by seal to the providers the chain assigned.

The deal must already exist (orama storage create, with the roots seal printed).
Every file's piece root is checked against its slot on chain before any byte
is sent, so a wrong file or a wrong deal uploads nothing. The command waits
for each slot to be assigned and for its provider to accept the root. The
provider endpoint is the node's first http(s) endpoint in x/nodes.`,
		Args: cobra.NoArgs,
		RunE: runPut,
	}
	put.Flags().Uint64("deal-id", 0, "Deal id")
	put.Flags().String("dir", "", "Directory holding slot-N files from seal")
	put.Flags().String("rpc", "", "oramad CometBFT RPC, for example http://127.0.0.1:31001")
	put.Flags().Duration("wait", storageclient.DefaultWait, "How long to wait for assignment and acceptance")
	for _, name := range []string{"deal-id", "dir", "rpc"} {
		_ = put.MarkFlagRequired(name)
	}
	Cmd.AddCommand(put)

	get := &cobra.Command{
		Use:   "get",
		Short: "Fetch and open a private file from its providers",
		Long: `Fetch the first slot of a deal that a provider serves with the on-chain
piece root, strip its slot layer, and decrypt it. A wrong seed or repair seed
fails and writes nothing.`,
		Args: cobra.NoArgs,
		RunE: runGet,
	}
	get.Flags().Uint64("deal-id", 0, "Deal id")
	get.Flags().String("rpc", "", "oramad CometBFT RPC, for example http://127.0.0.1:31001")
	get.Flags().String("seed", "", "Owner seed, hex, at least 32 bytes")
	get.Flags().String("repair-seed", "", "Repair seed, hex, at least 32 bytes")
	get.Flags().String("out", "", "Plaintext output file")
	for _, name := range []string{"deal-id", "rpc", "seed", "repair-seed", "out"} {
		_ = get.MarkFlagRequired(name)
	}
	Cmd.AddCommand(get)
}

func storageClient(cmd *cobra.Command, wait bool) (*storageclient.Client, error) {
	rpc, _ := cmd.Flags().GetString("rpc")
	chain, err := storageclient.NewChain(rpc)
	if err != nil {
		return nil, clierr.Usage("%v", err)
	}
	d := storageclient.DefaultWait
	if wait {
		d, _ = cmd.Flags().GetDuration("wait")
	}
	return storageclient.New(chain, &http.Client{}, d)
}

func runPut(cmd *cobra.Command, _ []string) error {
	dealID, _ := cmd.Flags().GetUint64("deal-id")
	dir, _ := cmd.Flags().GetString("dir")
	client, err := storageClient(cmd, true)
	if err != nil {
		return err
	}
	var slots [][]byte
	for i := 0; ; i++ {
		body, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("slot-%d", i)))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("read slot %d: %w", i, err)
		}
		slots = append(slots, body)
	}
	if len(slots) == 0 {
		return clierr.Usage("%s holds no slot-0 file", dir)
	}
	if err := client.Put(cmd.Context(), dealID, slots); err != nil {
		return clierr.Failure("%v", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "uploaded %d slots of deal %d\n", len(slots), dealID)
	return nil
}

func runGet(cmd *cobra.Command, _ []string) error {
	dealID, _ := cmd.Flags().GetUint64("deal-id")
	seed, repair, err := repairAndSeed(cmd)
	if err != nil {
		return clierr.Usage("%v", err)
	}
	out, _ := cmd.Flags().GetString("out")
	client, err := storageClient(cmd, false)
	if err != nil {
		return err
	}
	plain, err := client.Get(cmd.Context(), dealID, seed, repair)
	if err != nil {
		return clierr.Failure("%v", err)
	}
	if err := os.WriteFile(out, plain, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	return nil
}

func repairAndSeed(cmd *cobra.Command) (seed, repair []byte, err error) {
	seedHex, _ := cmd.Flags().GetString("seed")
	repairHex, _ := cmd.Flags().GetString("repair-seed")
	seed, err = hex.DecodeString(seedHex)
	if err != nil || len(seed) < minSeedLen {
		return nil, nil, fmt.Errorf("seed must be at least %d bytes of hex", minSeedLen)
	}
	repair, err = hex.DecodeString(repairHex)
	if err != nil || len(repair) < minSeedLen {
		return nil, nil, fmt.Errorf("repair seed must be at least %d bytes of hex", minSeedLen)
	}
	return seed, repair, nil
}

// minSeedLen is storagefile's minimum seed and repair-seed length.
const minSeedLen = 32
