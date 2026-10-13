package chainreach

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/netclass"
)

// expectedChainID is the chain id the environment is known to run (cli.ExpectedChainIDOf); a test
// replaces it.
var expectedChainID = cli.ExpectedChainIDOf

// Pin is the chain id a command run on the environment env signs for: the one the environment's
// registry network names, or explicit (--chain-id) when the environment is on no registry network.
// When both are given they must agree. With neither there is nothing to pin the wallet to, and the
// command is told to pass --chain-id: a node's own word for its chain is never enough.
func Pin(env, explicit string) (string, error) {
	if explicit != "" && !netclass.ValidChainID(explicit) {
		return "", clierr.Usage("--chain-id %q is not a chain id: 1 to %d characters of a-z, 0-9 and '-'", explicit, netclass.MaxChainIDLen)
	}
	pinned, network, err := expectedChainID(env)
	if err != nil {
		return "", clierr.Failure("%v", err)
	}
	switch {
	case pinned != "" && explicit != "" && explicit != pinned:
		return "", clierr.Usage("--chain-id %q is not the chain of network %q, which is %q", explicit, network, pinned)
	case pinned != "":
		return pinned, nil
	case explicit != "":
		return explicit, nil
	}
	return "", clierr.Usage("cannot tell which chain %q runs, so the wallet cannot be asked to sign for it: "+
		"put the environment on a registry network ('orama network use <name>') or pass --chain-id <id>", env)
}
