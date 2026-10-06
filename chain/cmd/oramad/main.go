// Command oramad is the Orama L1 node daemon.
package main

import (
	"fmt"
	"os"

	clientv2helpers "cosmossdk.io/client/v2/helpers"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad/cmd"
)

func main() {
	rootCmd := cmd.NewRootCmd()
	if err := svrcmd.Execute(rootCmd, clientv2helpers.EnvPrefix, app.DefaultNodeHome); err != nil {
		fmt.Fprintln(rootCmd.OutOrStderr(), err)
		os.Exit(1)
	}
}
