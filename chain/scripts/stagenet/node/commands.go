package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
)

func run(ctx context.Context, args []string, stdin io.Reader, _ io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: stagenet-node agent [flags]")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "agent":
		return cmdAgent(ctx, rest, stdin)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

type listenFlags []listenSpec

func (l *listenFlags) String() string { return fmt.Sprint(*l) }

func (l *listenFlags) Set(v string) error {
	spec, err := parseListen(v)
	if err != nil {
		return err
	}
	*l = append(*l, spec)
	return nil
}

func cmdAgent(ctx context.Context, args []string, stdin io.Reader) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	var listens listenFlags
	fs.Var(&listens, "listen", "socket to serve, as path:uid (repeatable)")
	ttl := fs.Duration("ttl", 0, "stop after this long, so an agent whose caller vanished does not keep the key in memory (0 is no limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ttl < 0 {
		return errors.New("--ttl must not be negative")
	}
	acct, err := accountFromStdin(stdin)
	if err != nil {
		return err
	}
	if *ttl > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *ttl)
		defer cancel()
	}
	return serveAgent(ctx, newAgentHandler(acct), listens)
}
