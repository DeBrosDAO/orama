package indexer

import "strconv"

// dasAsset is the DAS-style view of one asset record: the shape of a DAS
// getAsset answer, carrying only fields x/cnft has. There is no JSON
// metadata body (the chain stores a CID, not the document), no royalty or
// creators list (the collection holds those), and no Merkle proof.
type dasAsset struct {
	ID          string         `json:"id"`
	Burnt       bool           `json:"burnt"`
	Content     dasContent     `json:"content"`
	Compression dasCompression `json:"compression"`
	Grouping    []dasGroup     `json:"grouping"`
	Ownership   dasOwnership   `json:"ownership"`
	LastUpdate  dasLastUpdate  `json:"last_update"`
}

type dasContent struct {
	MetadataCID string `json:"metadata_cid"`
}

type dasCompression struct {
	Compressed  bool   `json:"compressed"`
	Tree        uint64 `json:"tree"`
	LeafID      uint32 `json:"leaf_id"`
	Nonce       uint64 `json:"nonce"`
	HashID      uint32 `json:"hash_id"`
	CreatorHash string `json:"creator_hash"`
	LeafHash    string `json:"leaf_hash,omitempty"`
}

type dasGroup struct {
	GroupKey   string `json:"group_key"`
	GroupValue string `json:"group_value"`
}

type dasOwnership struct {
	Owner     string `json:"owner"`
	Delegate  string `json:"delegate,omitempty"`
	Delegated bool   `json:"delegated"`
	State     string `json:"state"`
}

type dasLastUpdate struct {
	Height int64  `json:"height"`
	Tx     string `json:"tx"`
}

func toDAS(a Asset) dasAsset {
	return dasAsset{
		ID:      a.ID,
		Burnt:   a.State == AssetBurned,
		Content: dasContent{MetadataCID: a.MetadataCID},
		Compression: dasCompression{
			Compressed: a.State == AssetCompressed, Tree: a.TreeID, LeafID: a.LeafIndex,
			Nonce: a.Nonce, HashID: a.HashID, CreatorHash: a.CreatorHash, LeafHash: a.LeafHash,
		},
		Grouping:   []dasGroup{{GroupKey: "collection", GroupValue: strconv.FormatUint(a.CollectionID, 10)}},
		Ownership:  dasOwnership{Owner: a.Owner, Delegate: a.Delegate, Delegated: a.Delegate != "", State: a.State},
		LastUpdate: dasLastUpdate{Height: a.UpdatedHeight, Tx: a.UpdatedTx},
	}
}

func toDASList(in []Asset) []dasAsset {
	out := make([]dasAsset, 0, len(in))
	for _, a := range in {
		out = append(out, toDAS(a))
	}
	return out
}
