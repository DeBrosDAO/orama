package storagecmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/storageclient"
	"github.com/DeBrosOfficial/network/pkg/storagefile"
)

// Keys are the owner's two secrets a private deal needs, each read from a file
// (never from the command line).
type Keys struct {
	// StorageKeyFile holds the orama-storage-v1 key from RootWallet, hex.
	StorageKeyFile string
	// RepairSeedFile holds the repair seed, hex.
	RepairSeedFile string
}

func (k Keys) load() (storageKey, repair []byte, err error) {
	storageKey, err = storageKeyAt("storage-key-file", k.StorageKeyFile)
	if err != nil {
		return nil, nil, err
	}
	repair, err = secretAt("repair-seed-file", k.RepairSeedFile, "repair seed")
	if err != nil {
		return nil, nil, err
	}
	return storageKey, repair, nil
}

// SealSlots seals plain into one ciphertext per replica of a deal with the
// given nonce, exactly as `orama storage seal` does.
func SealSlots(keys Keys, nonceHex string, replicas int, plain []byte) ([]storagefile.Slot, error) {
	if replicas < storagefile.MinReplicas || replicas > storagefile.MaxReplicas {
		return nil, fmt.Errorf("replicas must be from %d to %d, got %d", storagefile.MinReplicas, storagefile.MaxReplicas, replicas)
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil || len(nonce) != storagefile.DealNonceLen {
		return nil, fmt.Errorf("the deal nonce must be %d bytes of hex", storagefile.DealNonceLen)
	}
	storageKey, repair, err := keys.load()
	if err != nil {
		return nil, err
	}
	return storagefile.Prepare(storageKey, repair, nonce, replicas, plain)
}

// WriteSlots writes slot-N files into dir and prints one "slot N root R" line
// each, the lines `orama storage seal` prints.
func WriteSlots(dir string, slots []storagefile.Slot, out io.Writer) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	for _, slot := range slots {
		path := filepath.Join(dir, fmt.Sprintf("slot-%d", slot.Index))
		if err := os.WriteFile(path, slot.Bytes, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Fprintf(out, "slot %d root %s\n", slot.Index, hex.EncodeToString(slot.Root))
	}
	return nil
}

// FetchPrivate reads a private deal back from its providers: the first slot a
// provider serves with the on-chain root, opened with the owner's keys. A
// wrong key or seed fails and returns nothing.
func FetchPrivate(ctx context.Context, rpc string, dealID uint64, keys Keys) ([]byte, error) {
	storageKey, repair, err := keys.load()
	if err != nil {
		return nil, err
	}
	chain, err := storageclient.NewChain(rpc)
	if err != nil {
		return nil, fmt.Errorf("connect to the chain at --rpc: %w", err)
	}
	client, err := storageclient.New(chain, storageclient.PublicHTTPClient(transferTimeout), storageclient.DefaultWait)
	if err != nil {
		return nil, fmt.Errorf("create the storage client: %w", err)
	}
	plain, err := client.Get(ctx, dealID, storageKey, repair)
	if err != nil {
		return nil, fmt.Errorf("fetch deal %d: %w", dealID, err)
	}
	return plain, nil
}
