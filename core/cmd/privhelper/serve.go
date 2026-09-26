package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

const (
	// requestReadTimeout bounds how long a client may take to send its request.
	requestReadTimeout = 30 * time.Second
	// responseWriteTimeout bounds how long a client may take to read the answer.
	responseWriteTimeout = 30 * time.Second
	// allowedUser is the account whose processes may make requests, besides root.
	allowedUser = "orama"
)

// serve handles the one connection systemd passed on stdin, and returns the
// process exit status. Every request is logged to the journal with the
// caller's uid, unit and argv, which is the audit trail of root actions.
//
// A caller must be root or the orama user (allowedCaller), and what it may ask
// for depends on the systemd unit it runs in (privhelper.Authorize).
func serve() int {
	conn, err := net.FileConn(os.NewFile(0, "privhelper-conn"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "not started by the socket: %v\n", err)
		return privhelper.ExitRefused
	}
	defer conn.Close()
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		fmt.Fprintf(os.Stderr, "stdin is not a unix socket\n")
		return privhelper.ExitRefused
	}

	caller, err := identifyCaller(uc)
	if err != nil {
		respond(uc, refusal(fmt.Errorf("cannot identify the caller: %w", err)))
		fmt.Fprintf(os.Stderr, "cannot identify the caller: %v\n", err)
		return privhelper.ExitRefused
	}
	uid := caller.UID
	if err := allowedCaller(uid); err != nil {
		respond(uc, refusal(err))
		fmt.Fprintf(os.Stderr, "uid=%d refused: %v\n", uid, err)
		return privhelper.ExitRefused
	}

	var req privhelper.Request
	uc.SetReadDeadline(time.Now().Add(requestReadTimeout))
	if err := json.NewDecoder(io.LimitReader(uc, privhelper.MaxRequestBytes)).Decode(&req); err != nil {
		respond(uc, refusal(fmt.Errorf("unreadable request: %w", err)))
		return privhelper.ExitRefused
	}

	resp, err := handle(caller, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "uid=%d unit=%q argv=%q refused: %v\n", uid, caller.Unit, req.Argv, err)
		respond(uc, resp)
		return privhelper.ExitRefused
	}

	fmt.Fprintf(os.Stderr, "uid=%d unit=%q argv=%q exit=%d\n", uid, caller.Unit, req.Argv, resp.ExitCode)
	respond(uc, resp)
	return 0
}

// handle decides a request: validate it, then Authorize, and only then run it.
// A refusal is the error; the response is what the client is sent either way.
func handle(caller privhelper.Caller, req privhelper.Request) (privhelper.Response, error) {
	inv, err := privhelper.Validate(req.Argv)
	if err == nil && !inv.NeedsInput() && req.Input != "" {
		err = fmt.Errorf("%v takes no input", req.Argv)
	}
	if err == nil {
		err = privhelper.Authorize(caller, inv)
	}
	if err != nil {
		return refusal(err), err
	}
	return execute(inv, []byte(req.Input)), nil
}

// allowedCaller admits root and the orama user.
func allowedCaller(uid uint32) error {
	if uid == 0 {
		return nil
	}
	u, err := user.Lookup(allowedUser)
	if err != nil {
		return fmt.Errorf("look up the %s user: %w", allowedUser, err)
	}
	if strconv.FormatUint(uint64(uid), 10) != u.Uid {
		return fmt.Errorf("uid %d is not root or %s", uid, allowedUser)
	}
	return nil
}

func refusal(err error) privhelper.Response {
	return privhelper.Response{ExitCode: privhelper.ExitRefused, Output: "orama-privhelper: refused: " + err.Error() + "\n"}
}

func respond(uc *net.UnixConn, resp privhelper.Response) {
	uc.SetWriteDeadline(time.Now().Add(responseWriteTimeout))
	if err := json.NewEncoder(uc).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "write response: %v\n", err)
	}
}
