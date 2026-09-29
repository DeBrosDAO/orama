package globalnode

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
)

const (
	// ChainRPCWaitBudget is how long a start waits for the chain RPC. oramad
	// replays its last blocks before the RPC listens.
	ChainRPCWaitBudget = 5 * time.Minute
	// rpcPollInterval is the time between two RPC probes.
	rpcPollInterval = 2 * time.Second
	// rpcProbeTimeout bounds one probe.
	rpcProbeTimeout = 2 * time.Second
)

// WaitChainRPC polls the chain's loopback CometBFT /status until it answers
// 200, for at most ChainRPCWaitBudget or until ctx is done.
func WaitChainRPC(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, ChainRPCWaitBudget)
	defer cancel()
	return pollHTTP(ctx, constants.LocalChainRPCURL()+"/status", rpcPollInterval)
}

// WaitChainRPCInNamespace is WaitChainRPC for a co-located node, whose chain
// runs in the orama-global network namespace: the RPC listens on that
// namespace's loopback, so the probe joins the namespace to dial it.
func WaitChainRPCInNamespace(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, ChainRPCWaitBudget)
	defer cancel()
	client := &http.Client{
		Timeout:   rpcProbeTimeout,
		Transport: &http.Transport{DialContext: globalnetns.DialContext(globalnetns.Path), DisableKeepAlives: true},
	}
	return pollHTTPWith(ctx, client, constants.LocalChainRPCURL()+"/status", rpcPollInterval)
}

// pollHTTP GETs url every interval until it answers 200 or ctx is done.
func pollHTTP(ctx context.Context, url string, interval time.Duration) error {
	return pollHTTPWith(ctx, &http.Client{Timeout: rpcProbeTimeout}, url, interval)
}

func pollHTTPWith(ctx context.Context, client *http.Client, url string, interval time.Duration) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var last error
	for {
		last = probe(ctx, client, url)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not answer: %w (last: %v)", url, ctx.Err(), last)
		case <-tick.C:
		}
	}
}

func probe(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
