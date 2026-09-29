// Command stagenet-node is the small helper the stagenet deploy runs on a stagenet node, as root,
// inside the orama-global network namespace, for the steps the `orama` and `oramad` CLIs cannot do
// (chain/scripts/stagenet/deploy.sh):
//
//	agent              serve a RootWallet-compatible signing socket for the operator key
//	address            print the orama address of a hot-key file
//	epoch              print x/emission's current epoch
//	earnings           print an account's earnings balance
//	fee-balance        print an account's fee-only balance (a hot key's)
//	register-operator  submit MsgRegisterOperator for the operator key
//	fund-hot-key       submit MsgFundHotKey for the operator key
//	node-status        print key=value lines about one x/nodes node
//
// The operator key is the validator key in oramad's test keyring (stagenet only, see deploy.sh). It
// is read from stdin, piped straight from `oramad keys export` on the same host, so it never leaves
// the node and never touches a disk.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "stagenet-node:", err)
		os.Exit(1)
	}
}
