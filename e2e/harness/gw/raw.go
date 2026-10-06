package gw

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

// RawBudget bounds a raw exchange.
const RawBudget = 20 * time.Second

// Raw writes request bytes exactly as given over a fresh TLS connection to the
// client's host and returns whatever comes back until the server closes the
// connection or RawBudget passes. It is for requests net/http refuses to send:
// malformed request lines, conflicting Content-Length and Transfer-Encoding,
// header folding, bare LF line endings. A pinned client dials its node; a
// request line naming a credential route waits on the pacer's address bucket.
func (c *Client) Raw(ctx context.Context, request []byte) ([]byte, error) {
	if c.pinErr != nil {
		return nil, c.pinErr
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base URL %q: %w", c.BaseURL, err)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	if c.TLS == nil {
		return nil, fmt.Errorf("raw exchange with %s needs the client's TLS config, and it has none", host)
	}
	target, err := c.dialTarget(host)
	if err != nil {
		return nil, err
	}
	if err := c.paceRaw(ctx, u.Hostname(), request); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, RawBudget)
	defer cancel()
	cfg := c.TLS.Clone()
	cfg.ServerName = u.Hostname()
	cfg.NextProtos = []string{"http/1.1"}
	start := time.Now()
	out, err := rawExchange(ctx, target, cfg, request)
	rec := evidence.Record{Kind: evidence.KindHTTP, Test: c.test, Summary: "RAW " + host + c.pinNote(),
		DurationMS: time.Since(start).Milliseconds(), Input: string(request), Output: string(out)}
	if err != nil {
		rec.Error = err.Error()
	}
	if recErr := c.rec.Add(rec); recErr != nil {
		return out, errors.Join(err, fmt.Errorf("failed to record raw exchange: %w", recErr))
	}
	return out, err
}

func rawExchange(ctx context.Context, host string, cfg *tls.Config, request []byte) ([]byte, error) {
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", host, err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("failed to set deadline on %s: %w", host, err)
	}
	if _, err := conn.Write(request); err != nil {
		return nil, fmt.Errorf("failed to write raw request to %s: %w", host, err)
	}
	out, err := io.ReadAll(io.LimitReader(conn, MaxResponseBytes))
	var ne net.Error
	if err != nil && errors.As(err, &ne) && ne.Timeout() && len(out) > 0 {
		// A keep-alive server answered and kept the connection open: what it
		// answered is the result.
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("failed to read raw response from %s: %w", host, err)
	}
	return out, nil
}
