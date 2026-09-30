//go:build e2e_fleet

package chain

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Status is the part of CometBFT's /status the tests read.
type Status struct {
	Network    string
	NodeID     string
	ListenAddr string
	Height     int64
	AppHash    string
	CatchingUp bool
	VotingPow  int64
}

// Comet fetches a CometBFT RPC path on n's loopback and decodes result.
func (c *Chain) Comet(t testing.TB, n fleet.Node, path string, result any) error {
	t.Helper()
	out := c.Run(t, n, QueryBudget, "curl -sS --max-time 10 "+fleet.ShellQuote(c.RPCHTTP()+path))
	if out.Exit != 0 {
		return fmt.Errorf("%s: GET %s exited %d: %s", n.Name, path, out.Exit, out.Stderr)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &env); err != nil {
		return fmt.Errorf("%s: GET %s: %w: %s", n.Name, path, err, out.Stdout)
	}
	if len(env.Error) > 0 && string(env.Error) != "null" {
		return fmt.Errorf("%s: GET %s: rpc error %s", n.Name, path, env.Error)
	}
	if err := json.Unmarshal(env.Result, result); err != nil {
		return fmt.Errorf("%s: GET %s result: %w", n.Name, path, err)
	}
	return nil
}

// NodeStatus reads CometBFT /status on n.
func (c *Chain) NodeStatus(t testing.TB, n fleet.Node) (Status, error) {
	t.Helper()
	var r struct {
		NodeInfo struct {
			ID         string `json:"id"`
			ListenAddr string `json:"listen_addr"`
			Network    string `json:"network"`
		} `json:"node_info"`
		SyncInfo struct {
			Height     Int    `json:"latest_block_height"`
			AppHash    string `json:"latest_app_hash"`
			CatchingUp bool   `json:"catching_up"`
		} `json:"sync_info"`
		ValidatorInfo struct {
			VotingPower Int `json:"voting_power"`
		} `json:"validator_info"`
	}
	if err := c.Comet(t, n, "/status", &r); err != nil {
		return Status{}, err
	}
	return Status{Network: r.NodeInfo.Network, NodeID: r.NodeInfo.ID, ListenAddr: r.NodeInfo.ListenAddr,
		Height: r.SyncInfo.Height.Int64(), AppHash: r.SyncInfo.AppHash, CatchingUp: r.SyncInfo.CatchingUp,
		VotingPow: r.ValidatorInfo.VotingPower.Int64()}, nil
}

// MustStatus is NodeStatus that fails the test on error.
func (c *Chain) MustStatus(t testing.TB, n fleet.Node) Status {
	t.Helper()
	s, err := c.NodeStatus(t, n)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Header is the identity of one block as a node stores it.
type Header struct {
	Height        int64
	BlockHash     string
	AppHash       string
	ChainID       string
	ProposerAddr  string
	LastBlockHash string
	Time          time.Time
}

// BlockHeader reads block height from n's store.
func (c *Chain) BlockHeader(t testing.TB, n fleet.Node, height int64) (Header, error) {
	t.Helper()
	var r struct {
		BlockID struct {
			Hash string `json:"hash"`
		} `json:"block_id"`
		Block struct {
			Header struct {
				ChainID         string    `json:"chain_id"`
				Height          Int       `json:"height"`
				AppHash         string    `json:"app_hash"`
				ProposerAddress string    `json:"proposer_address"`
				Time            time.Time `json:"time"`
				LastBlockID     struct {
					Hash string `json:"hash"`
				} `json:"last_block_id"`
			} `json:"header"`
		} `json:"block"`
	}
	if err := c.Comet(t, n, fmt.Sprintf("/block?height=%d", height), &r); err != nil {
		return Header{}, err
	}
	h := r.Block.Header
	return Header{Height: h.Height.Int64(), BlockHash: r.BlockID.Hash, AppHash: h.AppHash, ChainID: h.ChainID,
		ProposerAddr: h.ProposerAddress, LastBlockHash: h.LastBlockID.Hash, Time: h.Time}, nil
}

// WaitHeight waits until every validator has committed at least height.
func (c *Chain) WaitHeight(t testing.TB, height int64) {
	t.Helper()
	eventually.Require(t, PollEvery, EpochBudget, fmt.Sprintf("every validator to reach height %d", height), func() (bool, error) {
		for _, n := range c.Nodes() {
			s, err := c.NodeStatus(t, n)
			if err != nil {
				return false, err
			}
			if s.Height < height {
				return false, fmt.Errorf("%s is at %d", n.Name, s.Height)
			}
		}
		return true, nil
	})
}

// Height is the latest committed height node-1 reports.
func (c *Chain) Height(t testing.TB) int64 {
	t.Helper()
	return c.MustStatus(t, c.Node(t, 0)).Height
}
