package ipfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

const (
	// repoGCPath is Kubo's repo garbage collection RPC. stream-errors makes a
	// block it could not remove an entry of the stream, not only a log line.
	repoGCPath = "/api/v0/repo/gc?stream-errors=true"
	// bearerAuthPrefix starts the --api-auth value Kubo's CLI takes, which the
	// GC unit's environment file keeps: "bearer:<token>".
	bearerAuthPrefix = "bearer:"
	// maxGCErrors bounds how many failed blocks an error names.
	maxGCErrors = 5
)

// APIEndpoint turns the multiaddr of a Kubo RPC API (IPFS_API in a unit's
// environment, "/ip4/127.0.0.1/tcp/10102") and its --api-auth value
// ("bearer:<token>") into the base URL and bearer RepoGC takes. Only a TCP
// address and a bearer are accepted: they are the only form install writes.
// The address must be on this host (loopback, or the host-only address of the
// network namespace a co-located global node runs Kubo in): the request is
// plain HTTP and carries the bearer, so an address that leaves the host would
// send it across the network in cleartext.
func APIEndpoint(apiAddr, apiAuth string) (baseURL, token string, err error) {
	if apiAddr == "" {
		return "", "", errors.New("the Kubo API address is empty (IPFS_API)")
	}
	ma, err := multiaddr.NewMultiaddr(apiAddr)
	if err != nil {
		return "", "", fmt.Errorf("the Kubo API address %q is not a multiaddr: %w", apiAddr, err)
	}
	netAddr, err := manet.ToNetAddr(ma)
	if err != nil {
		return "", "", fmt.Errorf("the Kubo API address %q has no network address: %w", apiAddr, err)
	}
	addr, isTCP := netAddr.(*net.TCPAddr)
	if !isTCP {
		return "", "", fmt.Errorf("the Kubo API address %q is not a TCP address", apiAddr)
	}
	if !addr.IP.IsLoopback() && addr.IP.String() != constants.GlobalNetnsAddr {
		return "", "", fmt.Errorf("the Kubo API address %q is not on this host (loopback or %s), and the request carries the bearer in cleartext",
			apiAddr, constants.GlobalNetnsAddr)
	}
	token, ok := strings.CutPrefix(apiAuth, bearerAuthPrefix)
	if !ok || token == "" {
		return "", "", fmt.Errorf("the Kubo API credential (IPFS_API_AUTH) must be %s<token>", bearerAuthPrefix)
	}
	return "http://" + addr.String(), token, nil
}

// RepoGC garbage-collects the repo of the Kubo daemon whose RPC API is at
// baseURL and returns how many blocks it removed. It ends when ctx does: the
// request is cancelled, so a collection a caller stops is not left running
// behind a client that waits for it. A block Kubo failed to remove is an error
// once the stream has ended; the blocks removed before it stay removed.
func RepoGC(ctx context.Context, baseURL, token string) (int, error) {
	resp, err := PostAPI(ctx, baseURL+repoGCPath, token)
	if err != nil {
		return 0, fmt.Errorf("repo gc request to %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("repo gc at %s answered HTTP %d: %s", baseURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	removed := 0
	var failed []string
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var entry struct {
			Key   *json.RawMessage `json:"Key"`
			Error string           `json:"Error"`
		}
		if err := dec.Decode(&entry); err != nil {
			return removed, fmt.Errorf("read the repo gc stream of %s after %d blocks: %w", baseURL, removed, err)
		}
		switch {
		case entry.Error != "":
			failed = append(failed, entry.Error)
		case entry.Key != nil:
			removed++
		}
	}
	if len(failed) > 0 {
		return removed, fmt.Errorf("repo gc removed %d blocks and failed on %d: %s", removed, len(failed), summarizeGCErrors(failed))
	}
	return removed, nil
}

func summarizeGCErrors(failed []string) string {
	if len(failed) <= maxGCErrors {
		return strings.Join(failed, "; ")
	}
	return strings.Join(failed[:maxGCErrors], "; ") + fmt.Sprintf("; and %d more", len(failed)-maxGCErrors)
}
