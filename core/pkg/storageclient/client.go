package storageclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/pieceroot"
	"github.com/DeBrosOfficial/network/pkg/storagefile"
)

const (
	// DefaultWait is how long Put waits for a slot to be assigned and for its
	// provider to read that assignment.
	DefaultWait = 5 * time.Minute
	// defaultPoll is how often Put asks the chain or the provider again.
	defaultPoll   = 3 * time.Second
	transferLimit = 1 << 30
	rootHeader    = "X-Piece-Root"
)

var (
	// ErrRootMismatch is bytes whose piece root is not the root on chain.
	ErrRootMismatch = errors.New("piece root does not match the chain")
	// ErrNoReplica is a Get where no slot could be fetched and opened.
	ErrNoReplica = errors.New("no replica could be fetched")
)

// Client moves sealed slots between this machine and the assigned providers.
type Client struct {
	chain *Chain
	http  *http.Client
	wait  time.Duration
	poll  time.Duration
}

// New returns a client that reads the chain through chain.
func New(chain *Chain, httpClient *http.Client, wait time.Duration) (*Client, error) {
	if chain == nil || httpClient == nil {
		return nil, errors.New("storage client needs a chain and an http client")
	}
	if wait <= 0 {
		return nil, errors.New("wait must be positive")
	}
	return &Client{chain: chain, http: httpClient, wait: wait, poll: defaultPoll}, nil
}

// Put uploads slots[i] to the provider assigned slot i of dealID. Every
// slot's piece root is checked against the chain before any byte is sent,
// so a wrong file or a wrong deal uploads nothing.
func (c *Client) Put(ctx context.Context, dealID uint64, slots [][]byte) error {
	deal, err := c.chain.Deal(ctx, dealID)
	if err != nil {
		return err
	}
	if uint32(len(slots)) != deal.Replicas {
		return fmt.Errorf("deal %d has %d replicas, got %d slot files", dealID, deal.Replicas, len(slots))
	}
	ctx, cancel := context.WithTimeout(ctx, c.wait)
	defer cancel()
	assigned := make([]Slot, len(slots))
	for i, body := range slots {
		slot, err := c.waitAssigned(ctx, dealID, uint32(i))
		if err != nil {
			return err
		}
		if err := checkRoot(body, slot.PieceRoot); err != nil {
			return fmt.Errorf("slot %d: %w", i, err)
		}
		assigned[i] = slot
	}
	for i, slot := range assigned {
		if slot.Accepted {
			continue
		}
		base, err := c.chain.ProviderURL(ctx, slot.NodeID)
		if err != nil {
			return err
		}
		if err := c.upload(ctx, base, slot.PieceRoot, slots[i]); err != nil {
			return fmt.Errorf("slot %d to %s: %w", i, slot.NodeID, err)
		}
	}
	return nil
}

func (c *Client) waitAssigned(ctx context.Context, dealID uint64, index uint32) (Slot, error) {
	for {
		slot, err := c.chain.Slot(ctx, dealID, index)
		if err != nil {
			return Slot{}, err
		}
		if slot.Held() {
			return slot, nil
		}
		if err := sleep(ctx, c.poll); err != nil {
			return Slot{}, fmt.Errorf("deal %d slot %d was not assigned in time: %w", dealID, index, err)
		}
	}
}

// upload retries a 403: the provider refuses a root until its runner has
// read the assignment from the chain.
func (c *Client) upload(ctx context.Context, base string, root, body []byte) error {
	name := hex.EncodeToString(root)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/pieces/"+name, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set(rootHeader, name)
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNoContent, http.StatusOK:
			return nil
		case http.StatusForbidden:
			if err := sleep(ctx, c.poll); err != nil {
				return fmt.Errorf("provider did not accept the root in time: %w", err)
			}
		default:
			return fmt.Errorf("provider answered HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
		}
	}
}

// Get fetches the first slot of dealID that a provider serves with the
// on-chain root, strips its slot layer and decrypts it. A wrong seed or
// repair seed returns storagefile.ErrNotForKey and nothing else is tried.
func (c *Client) Get(ctx context.Context, dealID uint64, seed, repairSeed []byte) ([]byte, error) {
	deal, err := c.chain.Deal(ctx, dealID)
	if err != nil {
		return nil, err
	}
	var errs []error
	for i := uint32(0); i < deal.Replicas; i++ {
		body, err := c.fetchSlot(ctx, dealID, i)
		if err != nil {
			errs = append(errs, fmt.Errorf("slot %d: %w", i, err))
			continue
		}
		plain, err := storagefile.Open(seed, repairSeed, deal.Nonce, i, body)
		if err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
		return plain, nil
	}
	return nil, fmt.Errorf("%w for deal %d: %w", ErrNoReplica, dealID, errors.Join(errs...))
}

func (c *Client) fetchSlot(ctx context.Context, dealID uint64, index uint32) ([]byte, error) {
	slot, err := c.chain.Slot(ctx, dealID, index)
	if err != nil {
		return nil, err
	}
	if !slot.Held() || !slot.Accepted {
		return nil, errors.New("no provider holds this slot")
	}
	base, err := c.chain.ProviderURL(ctx, slot.NodeID)
	if err != nil {
		return nil, err
	}
	return Fetch(ctx, c.http, base, slot.PieceRoot)
}

// Fetch reads one piece from a provider and returns it only when its piece
// root is root.
func Fetch(ctx context.Context, client *http.Client, base string, root []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/pieces/"+hex.EncodeToString(root), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, transferLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read piece: %w", err)
	}
	if len(body) > transferLimit {
		return nil, fmt.Errorf("piece is larger than %d bytes", transferLimit)
	}
	if err := checkRoot(body, root); err != nil {
		return nil, err
	}
	return body, nil
}

func checkRoot(body, root []byte) error {
	got, err := pieceroot.Commit(body)
	if err != nil {
		return fmt.Errorf("piece root: %w", err)
	}
	if !bytes.Equal(got.Root, root) {
		return ErrRootMismatch
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
