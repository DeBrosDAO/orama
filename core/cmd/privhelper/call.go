package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

const (
	dialTimeout = 5 * time.Second
	// callTimeout covers the slowest request: a systemctl start that waits
	// for its unit, within the service's RuntimeMaxSec.
	callTimeout = 10 * time.Minute
)

// call sends argv to the root helper and relays its output and exit status.
// It is validated here too, so a caller learns of a refusal without a round
// trip; the helper validates again and is the one that decides.
func call(argv []string) int {
	inv, err := privhelper.Validate(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orama-privhelper: refused: %v\n", err)
		return privhelper.ExitRefused
	}
	input, err := readInput(inv, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orama-privhelper: %v\n", err)
		return privhelper.ExitRefused
	}

	conn, err := net.DialTimeout("unix", privhelper.SocketPath, dialTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orama-privhelper: cannot reach %s: %v (is %s running? systemctl status %s)\n",
			privhelper.SocketPath, err, privhelper.SocketUnitName, privhelper.SocketUnitName)
		return privhelper.ExitRefused
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(callTimeout))

	if err := json.NewEncoder(conn).Encode(privhelper.Request{Argv: argv, Input: string(input)}); err != nil {
		fmt.Fprintf(os.Stderr, "orama-privhelper: send request: %v\n", err)
		return privhelper.ExitRefused
	}
	var resp privhelper.Response
	if err := json.NewDecoder(io.LimitReader(conn, privhelper.MaxRequestBytes)).Decode(&resp); err != nil {
		fmt.Fprintf(os.Stderr, "orama-privhelper: read response: %v\n", err)
		return privhelper.ExitRefused
	}
	fmt.Print(resp.Output)
	return resp.ExitCode
}
