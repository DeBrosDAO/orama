// Command orama-privhelper runs one allow-listed root command on behalf of the
// unprivileged orama user. See pkg/privhelper for what it allows and why.
//
//	orama-privhelper serve            socket-activated: one request on stdin (as root)
//	orama-privhelper call <tool> ...  client: send a request to the socket
//	orama-privhelper run <tool> ...   run a request directly (as root)
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: orama-privhelper serve | call <tool> <args...> | run <tool> <args...> | verify-deploy-dir <instance>")
	}
	switch mode, argv := os.Args[1], os.Args[2:]; mode {
	case "serve":
		os.Exit(serve())
	case "call":
		os.Exit(call(argv))
	case "verify-deploy-dir":
		if os.Geteuid() != 0 {
			fail("verify-deploy-dir must be root; it is ExecStartPre of orama-deploy-*@")
		}
		if len(argv) != 1 {
			fail("usage: orama-privhelper verify-deploy-dir <instance>")
		}
		dir, err := privhelper.DeploymentDirForInstance(argv[0])
		if err != nil {
			fail("%v", err)
		}
		if err := verifyDeploymentDir(rootfs.At(privhelper.DeploymentsAnchor), dir, allowedUser); err != nil {
			fail("%v", err)
		}
	case "run":
		if os.Geteuid() != 0 {
			fail("run must be root; use call")
		}
		inv, err := privhelper.Validate(argv)
		if err != nil {
			fail("refused: %v", err)
		}
		input, err := readInput(inv, os.Stdin)
		if err != nil {
			fail("%v", err)
		}
		resp := execute(inv, input)
		fmt.Print(resp.Output)
		os.Exit(resp.ExitCode)
	default:
		fail("unknown mode %q", mode)
	}
}

// readInput reads the payload an invocation takes, and nothing otherwise.
func readInput(inv privhelper.Invocation, r io.Reader) ([]byte, error) {
	if !inv.NeedsInput() {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, privhelper.MaxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	if len(data) > privhelper.MaxRequestBytes {
		return nil, fmt.Errorf("input is over %d bytes", privhelper.MaxRequestBytes)
	}
	return data, nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "orama-privhelper: "+format+"\n", args...)
	os.Exit(privhelper.ExitRefused)
}
