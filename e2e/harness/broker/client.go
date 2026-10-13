package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Client talks to the run's broker.
type Client struct {
	sock string
}

// New returns a client for the broker listening on sock (absolute).
func New(sock string) (*Client, error) {
	if !filepath.IsAbs(sock) {
		return nil, fmt.Errorf("%s=%q must be an absolute socket path", EnvSock, sock)
	}
	return &Client{sock: sock}, nil
}

// FromEnv is the broker of the run this process belongs to: nil (and no
// error) when E2E_BROKER_SOCK is unset, which is the runner itself or a
// process outside a run.
func FromEnv(lookup func(string) (string, bool)) (*Client, error) {
	v, ok := lookup(EnvSock)
	if !ok || strings.TrimSpace(v) == "" {
		return nil, nil
	}
	return New(strings.TrimSpace(v))
}

// SetTXT creates the TXT record name = value in the run's zone.
func (c *Client) SetTXT(ctx context.Context, name, value string) error {
	_, err := c.call(ctx, Request{Op: OpTXTSet, Name: name, Value: value})
	return err
}

// DeleteTXT deletes the TXT records of name holding value (all when empty).
func (c *Client) DeleteTXT(ctx context.Context, name, value string) error {
	_, err := c.call(ctx, Request{Op: OpTXTDelete, Name: name, Value: value})
	return err
}

// Records lists the run's records named under (or under it); every record
// of the run when under is empty.
func (c *Client) Records(ctx context.Context, under string) ([]Record, error) {
	resp, err := c.call(ctx, Request{Op: OpRecordsList, Name: under})
	return resp.Records, err
}

// AddExtra creates an extra server of the run (provision.AddExtra).
func (c *Client) AddExtra(ctx context.Context, name, location string) (fleet.Node, error) {
	resp, err := c.call(ctx, Request{Op: OpExtraAdd, Name: name, Location: location})
	if err != nil {
		return fleet.Node{}, err
	}
	if resp.Node == nil {
		return fleet.Node{}, fmt.Errorf("the broker created extra %s but returned no node", name)
	}
	return *resp.Node, nil
}

// RemoveExtra deletes an extra server of the run (provision.RemoveExtra).
func (c *Client) RemoveExtra(ctx context.Context, name string) error {
	_, err := c.call(ctx, Request{Op: OpExtraRemove, Name: name})
	return err
}

// AddCluster installs a single-node eval cluster (provision.AddEvalCluster).
func (c *Client) AddCluster(ctx context.Context, name string) (fleet.Cluster, error) {
	resp, err := c.call(ctx, Request{Op: OpClusterAdd, Name: name})
	if err != nil {
		return fleet.Cluster{}, err
	}
	if resp.Cluster == nil {
		return fleet.Cluster{}, fmt.Errorf("the broker installed cluster %s but returned nothing", name)
	}
	return *resp.Cluster, nil
}

// RemoveCluster removes an eval cluster (provision.RemoveEvalCluster).
func (c *Client) RemoveCluster(ctx context.Context, name string) error {
	_, err := c.call(ctx, Request{Op: OpClusterRemove, Name: name})
	return err
}

// call sends req and reads the answer. Ending ctx closes the connection,
// which cancels the operation on the server.
func (c *Client) call(ctx context.Context, req Request) (Response, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.sock)
	if err != nil {
		return Response{}, fmt.Errorf("broker %s: failed to connect to %s (is e2e-fleet run/test serving it?): %w", req.Op, c.sock, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	line, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("broker %s: failed to encode the request: %w", req.Op, err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return Response{}, fmt.Errorf("broker %s %s: failed to send: %w", req.Op, req.Name, errors.Join(err, ctx.Err()))
	}
	var resp Response
	if err := readLine(conn, &resp); err != nil {
		return Response{}, fmt.Errorf("broker %s %s: no answer: %w", req.Op, req.Name, errors.Join(err, ctx.Err()))
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("broker %s %s: %s", req.Op, req.Name, resp.Error)
	}
	return resp, nil
}

// readLine decodes one bounded JSON line from r into v.
func readLine(r io.Reader, v any) error {
	br := bufio.NewReaderSize(io.LimitReader(r, maxMessageBytes+1), maxMessageBytes+1)
	raw, err := br.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("failed to read a message line: %w", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("failed to decode a message: %w", err)
	}
	return nil
}
