package statesync

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	// TrustHeightMargin is how far below the seeds' newest block the trusted
	// height is taken. A commit is signed by the next block, and a seed that
	// is a few blocks behind the other has not got the newest yet; a hundred
	// blocks (ten minutes) is far enough that both have it and near enough that
	// the trust period of seven days is nowhere close.
	TrustHeightMargin = 100
	// MinSeeds is how many independent seeds a trust point needs.
	MinSeeds = 2
)

// Peer is a node of the network a joiner dials.
type Peer struct {
	NodeID string
	// Host is the seed's DNS name.
	Host string
}

// TrustPoint is what a joiner starts from.
type TrustPoint struct {
	Height int64
	Hash   string
	// Servers are the light-client endpoints for [statesync] rpc_servers.
	Servers []string
	// Peers are the seeds with their node ids, for persistent peers.
	Peers []Peer
}

// PersistentPeers renders the peers as id@host:port,... for the chain unit.
func (t TrustPoint) PersistentPeers() string {
	parts := make([]string, len(t.Peers))
	for i, p := range t.Peers {
		parts[i] = p.NodeID + "@" + p.Host + ":" + strconv.Itoa(constants.ChainP2PPort)
	}
	return strings.Join(parts, ",")
}

// Resolve asks every seed (DNS names from the network's manifest) for its
// status and for the block at the same height, and returns that block as the
// trust point. It fails unless at least MinSeeds seeds answer, every one is on
// chainID and caught up, and they agree on the hash: a seed that disagrees is
// a fork or an attacker, and a joiner must not pick between them.
func Resolve(ctx context.Context, seeds []string, chainID string, hc Doer) (*TrustPoint, error) {
	if len(seeds) < MinSeeds {
		return nil, fmt.Errorf("state sync needs %d independent seeds and the network lists %d", MinSeeds, len(seeds))
	}
	clients := make([]Client, len(seeds))
	statuses := make([]Status, len(seeds))
	lowest := int64(0)
	for i, seed := range seeds {
		clients[i] = Client{BaseURL: "https://" + seed, HTTP: hc}
		st, err := clients[i].Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("seed %s: %w", seed, err)
		}
		if st.Network != chainID {
			return nil, fmt.Errorf("seed %s is on chain %q, but the network is %q: the manifest and the seed disagree", seed, st.Network, chainID)
		}
		if st.CatchingUp {
			return nil, fmt.Errorf("seed %s is still catching up (height %d): try again when it has", seed, st.LatestHeight)
		}
		statuses[i] = st
		if lowest == 0 || st.LatestHeight < lowest {
			lowest = st.LatestHeight
		}
	}
	height := lowest - TrustHeightMargin
	if height < 1 {
		return nil, fmt.Errorf("the chain is at height %d, below the %d blocks a trust point is taken behind the newest", lowest, TrustHeightMargin)
	}
	hash, err := agreedHash(ctx, seeds, clients, height)
	if err != nil {
		return nil, err
	}
	tp := &TrustPoint{Height: height, Hash: hash}
	for i, c := range clients {
		tp.Servers = append(tp.Servers, c.Endpoint())
		tp.Peers = append(tp.Peers, Peer{NodeID: statuses[i].NodeID, Host: seeds[i]})
	}
	if err := distinctHosts(tp.Servers); err != nil {
		return nil, err
	}
	return tp, nil
}

// agreedHash reads the block at height from every seed and returns its hash,
// when every seed gives the same one.
func agreedHash(ctx context.Context, seeds []string, clients []Client, height int64) (string, error) {
	var agreed string
	for i, c := range clients {
		hash, err := c.CommitHash(ctx, height)
		if err != nil {
			return "", fmt.Errorf("seed %s: %w", seeds[i], err)
		}
		if agreed != "" && hash != agreed {
			return "", fmt.Errorf("seeds %s and %s give different blocks at height %d (%s and %s): refusing to pick one", seeds[0], seeds[i], height, agreed, hash)
		}
		agreed = hash
	}
	return agreed, nil
}

// distinctHosts refuses two servers on one host: two names that resolve to the
// same gateway are one witness, not two.
func distinctHosts(servers []string) error {
	seen := map[string]bool{}
	for _, s := range servers {
		u, err := url.Parse(s)
		if err != nil {
			return fmt.Errorf("light-client endpoint %q: %w", s, err)
		}
		if seen[u.Host] {
			return fmt.Errorf("the seed %s is listed twice: the servers must be independent", u.Host)
		}
		seen[u.Host] = true
	}
	return nil
}
