// Package indexer follows an oramad over its loopback CometBFT RPC and keeps a
// local index of blocks, transactions, an address → transaction index, and
// the compressed NFTs of x/cnft. It stores the index in Pebble (pure Go) and
// serves it read-only over HTTP (api.go).
//
// Everything is taken from what the chain commits: block headers, the raw
// transaction bytes, and each transaction's ExecTxResult (code, gas, events,
// message responses). x/cnft and x/market emit no events of their own, so
// cNFT state is rebuilt from the message bodies and their responses, the same
// way the chain rebuilds a leaf from the transaction that wrote it.
package indexer

import (
	"encoding/json"
	"time"
)

// Block is one indexed block. Hashes are lowercase hex.
type Block struct {
	Height   int64     `json:"height"`
	Hash     string    `json:"hash"`
	Time     time.Time `json:"time"`
	Proposer string    `json:"proposer"`
	TxCount  int       `json:"tx_count"`
	TxHashes []string  `json:"tx_hashes"`
	// GasUsed is the sum of the gas its transactions used.
	GasUsed int64 `json:"gas_used"`
	// Burned is the norama its transactions burned as base fee (the "base_fee"
	// attribute of each "tx" event). Finalize-block burns are not included.
	Burned string `json:"burned"`
}

// Attribute is one event attribute as the chain emitted it.
type Attribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Event is one ABCI event of a transaction.
type Event struct {
	Type       string      `json:"type"`
	Attributes []Attribute `json:"attributes"`
}

// Tx is one indexed transaction, failed ones included. Messages are the type
// URLs of the body's messages, in order. A transaction whose bytes are not a
// Cosmos transaction (the chain refused it with a non-zero code) has none.
type Tx struct {
	Hash      string   `json:"hash"`
	Height    int64    `json:"height"`
	Index     uint32   `json:"index"`
	Code      uint32   `json:"code"`
	Codespace string   `json:"codespace,omitempty"`
	Log       string   `json:"log,omitempty"`
	GasWanted int64    `json:"gas_wanted"`
	GasUsed   int64    `json:"gas_used"`
	Messages  []string `json:"messages"`
	Events    []Event  `json:"events"`
	// Time is the block's time.
	Time time.Time `json:"time"`
	// Signer is the account of the transaction's first signature, or empty for a
	// transaction with none (a shielded one) or whose bytes do not decode.
	Signer string `json:"signer,omitempty"`
	Memo   string `json:"memo,omitempty"`
	// Body is the transaction body as JSON: one object per message, each with its
	// "@type". A message whose type this build does not know is the "@type" alone.
	Body []json.RawMessage `json:"body"`
}

// AccountSummary is what the index knows of one account: how many
// transactions named it, and the time of the first and of the last.
type AccountSummary struct {
	Address    string    `json:"address"`
	TxCount    uint64    `json:"tx_count"`
	FirstSeen  time.Time `json:"first_seen"`
	LastActive time.Time `json:"last_active"`
}

// HourStat is the transactions of one UTC hour. Burned is base fee burned by
// transactions, in norama.
type HourStat struct {
	Hour   time.Time `json:"hour"`
	Txs    uint64    `json:"txs"`
	Failed uint64    `json:"failed"`
	Burned string    `json:"burned"`
}

// Asset states.
const (
	AssetCompressed   = "compressed"
	AssetDecompressed = "decompressed"
	AssetBurned       = "burned"
)

// Asset is one compressed NFT at one tree location. The chain does not make
// asset ids unique across mints, so the index keys an asset by its id, tree
// and leaf index; one id can have several records. Every field is a field of
// the chain's Leaf or DecompressedAsset, or the location that holds it.
// LeafHash is set only while the asset is a live compressed leaf.
type Asset struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	TreeID        uint64 `json:"tree_id"`
	LeafIndex     uint32 `json:"leaf_index"`
	CollectionID  uint64 `json:"collection_id"`
	Owner         string `json:"owner"`
	Delegate      string `json:"delegate,omitempty"`
	MetadataCID   string `json:"metadata_cid"`
	CreatorHash   string `json:"creator_hash"`
	Nonce         uint64 `json:"nonce"`
	HashID        uint32 `json:"hash_id"`
	LeafHash      string `json:"leaf_hash,omitempty"`
	UpdatedHeight int64  `json:"updated_height"`
	UpdatedTx     string `json:"updated_tx"`
}

// Status is the follower's position.
type Status struct {
	StartHeight int64 `json:"start_height"`
	Cursor      int64 `json:"cursor"`
}
