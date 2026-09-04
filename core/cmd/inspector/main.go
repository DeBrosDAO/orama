// Command inspector runs Orama's cluster health checks as a standalone binary.
//
// It is the same command as `orama inspect`, so both share one definition and
// cannot drift in flags or behaviour.
package main

import (
	"fmt"
	"os"

	inspectcmd "github.com/DeBrosOfficial/network/pkg/cli/cmd/inspectcmd"
)

func main() {
	if err := inspectcmd.Cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
