// Command stagenet-node is the small helper the stagenet smoke runs on a stagenet node, as root,
// inside the orama-global network namespace (chain/scripts/stagenet/deploy.sh smoke):
//
//	agent   serve a RootWallet-compatible signing socket for the operator key
//
// The operator key is the seat's "validator" key in oramad's test keyring (stagenet only). It is
// read from stdin, piped straight from `oramad keys export` on the same host, so it never leaves
// the node and never touches a disk.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// stopSignals end the helper cleanly, so an agent removes its sockets. SIGHUP is the one an agent
// gets when the ssh session that started it closes; unhandled, it killed the process before its
// cleanup ran and left a stale socket that the next run's readiness check mistook for the new one.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "stagenet-node:", err)
		os.Exit(1)
	}
}
