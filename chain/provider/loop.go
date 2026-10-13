package provider

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// Decide binds a stored piece whose root matches and returns the accept
// message, or a decline when this node does not hold that root. It does
// not submit either message.
func (s *Store) Decide(signer, nodeID string, dealID uint64, slot uint32, root []byte) (*types.MsgAcceptDeal, *types.MsgDeclineDeal, error) {
	if signer == "" {
		return nil, nil, errors.New("signer is empty")
	}
	if nodeID == "" {
		return nil, nil, errors.New("node id is empty")
	}
	if dealID == 0 {
		return nil, nil, errors.New("deal id is empty")
	}
	if len(root) != 32 {
		return nil, nil, errors.New("piece root is not 32 bytes")
	}
	cid, ok, err := s.FindByRoot(root)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, &types.MsgDeclineDeal{
			Signer: signer, NodeId: nodeID, DealId: dealID, Slot: slot, Reason: "piece not stored",
		}, nil
	}
	if err := s.Bind(cid, dealID, slot); err != nil {
		return nil, nil, err
	}
	return &types.MsgAcceptDeal{
		Signer: signer, NodeId: nodeID, DealId: dealID, Slot: slot,
	}, nil, nil
}

// FindByRoot returns the CID of one stored piece with this root.
func (s *Store) FindByRoot(root []byte) (string, bool, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "meta"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		cid := strings.TrimSuffix(name, ".json")
		if err := validCID(cid); err != nil {
			return "", false, fmt.Errorf("meta file %s: %w", name, err)
		}
		got, err := s.ReadRoot(cid)
		if err != nil {
			return "", false, err
		}
		if bytes.Equal(got, root) {
			return cid, true, nil
		}
	}
	return "", false, nil
}
