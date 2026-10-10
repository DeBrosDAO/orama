// Package faucetcmd is `orama maint faucet`: set up the public faucet of a test network on a node.
//
// A gateway serves POST /v1/chain/faucet when node.yaml turns chain.faucet on and the gateway can
// read a faucet key (pkg/chainfaucet). `init` makes that key, on the node, as root, so the secret
// is made where it is used and never travels.
package faucetcmd

import (
	"os"
	"os/user"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

// MaintCmd is `orama maint faucet`.
var MaintCmd = &cobra.Command{
	Use:   "faucet",
	Short: "Set up the public faucet of a test network on this node",
}

// init is node-local: it runs on the node, as root, reads and writes only that node's files, and has
// no operator environment (no gateway, no CA files) to load.
func init() { MaintCmd.AddCommand(cmdmeta.MarkNodeLocal(newInitCmd())) }

// initReport is what `init` prints.
type initReport struct {
	Address string `json:"address"`
	KeyFile string `json:"key_file"`
	Created bool   `json:"created"`
}

func newInitCmd() *cobra.Command {
	var keyFile, owner string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create this node's faucet key and print the account to fund",
		Long: `Create the key a node's gateway signs faucet drips with, and print the account
it belongs to. Run it on the node, as root.

The faucet gives test ORAMA to whoever asks (POST /v1/chain/faucet): the gateway
signs MsgFaucet for the recipient with this key and the chain mints the drip.
It exists only on a test network (a chain id with -stagenet-, -devnet- or
-localnet-; the gateway refuses to sign anywhere else), the chain keeps its own
limits (a maximum drip, a cooldown per recipient, a cap per epoch), and it must
be on in the genesis (faucet_enabled).

The key file is created owned by the gateway's account with mode 0600, and an
existing key is never replaced: running init again prints the same account. The
faucet account pays the transaction fee of every drip and mints the drip itself,
so it needs a small balance and nothing more: fund it from the genesis
(chain/scripts/stagenet/deploy.sh does this on stagenet) or from another faucet.

Then turn it on in node.yaml and restart the node:

  chain:
    faucet:
      enabled: true

  orama node restart

orama node upgrade keeps the block. Check it with:

  curl -sS -X POST https://<gateway>/v1/chain/faucet \
    -H 'Content-Type: application/json' -d '{"recipient":"orama1..."}'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep, err := initKey(keyFile, owner)
			if err != nil {
				return err
			}
			return printInit(printer.For(cmd), rep)
		},
	}
	f := cmd.Flags()
	f.StringVar(&keyFile, "key-file", constants.ChainFaucetKeyFile, "Where the key goes (node.yaml chain.faucet.key_file, when it is not this default)")
	f.StringVar(&owner, "owner", chainfaucet.GatewayUser, "The account that owns the key file: the one the gateway runs as")
	return cmd
}

// lookupUser is a seam the tests replace.
var lookupUser = user.Lookup

// initKey creates the key file for the owner account, or reads back the one that is there.
func initKey(keyFile, owner string) (initReport, error) {
	acct, err := lookupUser(owner)
	if err != nil {
		return initReport{}, clierr.Usage("--owner %q: %v (the gateway runs as the account the node install created, orama)", owner, err)
	}
	uid, err := strconv.Atoi(acct.Uid)
	if err != nil {
		return initReport{}, clierr.Failure("account %s has the user id %q, which is not a number", owner, acct.Uid)
	}
	gid, err := strconv.Atoi(acct.Gid)
	if err != nil {
		return initReport{}, clierr.Failure("account %s has the group id %q, which is not a number", owner, acct.Gid)
	}
	if os.Geteuid() != 0 && os.Geteuid() != uid {
		return initReport{}, clierr.Usage("run this as root (sudo): the key file is made for the %s account", owner)
	}
	key, created, err := chainfaucet.CreateKeyFile(keyFile, uid, gid)
	if err != nil {
		return initReport{}, clierr.Failure("%v", err)
	}
	return initReport{Address: key.Address(), KeyFile: keyFile, Created: created}, nil
}

func printInit(p *printer.Printer, rep initReport) error {
	if p.JSONMode() {
		return p.JSON(rep)
	}
	if rep.Created {
		p.Ok("created the faucet key %s", rep.KeyFile)
	} else {
		p.Info("the faucet key %s already exists; not replaced", rep.KeyFile)
	}
	p.Printf("faucet account: %s\n", rep.Address)
	p.Printf("fund it with norama for fees (a drip costs one transaction fee), then set chain.faucet.enabled: true in node.yaml and run `orama node restart`\n")
	if rep.KeyFile != constants.ChainFaucetKeyFile {
		p.Printf("this is not the default key file: also set chain.faucet.key_file: %s\n", rep.KeyFile)
	}
	return nil
}
