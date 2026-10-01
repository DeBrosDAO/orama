package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/chain/client/node"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// checkStoragePrivate runs the product path of a private storage deal from the operator's machine:
// `orama storage seal`, `create` (signed through the forwarded agent), `put`, `get` and `open`,
// with the chain's own state as the judge of assignment, acceptance and proofs.
func checkStoragePrivate(ctx context.Context, e *env) Result {
	c, err := e.client(ctx, e.nodes[0])
	if err != nil {
		return fail(storageName, "%v", err)
	}
	rpc, err := e.rpcOf(ctx, e.nodes[0])
	if err != nil {
		return fail(storageName, "%v", err)
	}
	rest, err := e.restOf(ctx, e.nodes[0])
	if err != nil {
		return fail(storageName, "%v", err)
	}
	dir, err := os.MkdirTemp(e.workDir, "storage")
	if err != nil {
		return fail(storageName, "%v", err)
	}
	var params storagetypes.QueryParamsResponse
	if err := c.Query(ctx, "/orama.storage.v1.Query/Params", &storagetypes.QueryParamsRequest{}, &params); err != nil {
		return fail(storageName, "read the storage params: %v", err)
	}
	size := int(max(params.Params.MinDealBytes, minPlaintextBytes))
	fx, err := newStorageFixture(dir, size)
	if err != nil {
		return fail(storageName, "%v", err)
	}
	agent := []string{"RW_AGENT_SOCK=" + e.agentSocket}

	out, err := runOrama(ctx, e.orama, nil, "storage", "seal", "--storage-key-file", fx.keyFile, "--repair-seed-file", fx.seedFile,
		"--nonce", fx.nonce, "--replicas", strconv.Itoa(privateReplicas), "--in", fx.plainFile, "--out-dir", fx.sealDir)
	if err != nil {
		return fail(storageName, "seal: %v", err)
	}
	roots, err := parseSealOutput(out)
	if err != nil {
		return fail(storageName, "%v", err)
	}
	sizes, err := slotSizes(fx.sealDir, len(roots))
	if err != nil {
		return fail(storageName, "%v", err)
	}
	specs, err := pieceSpecs(roots, sizes)
	if err != nil {
		return fail(storageName, "%v", err)
	}
	var bf feestypes.QueryBaseFeeResponse
	if err := c.Query(ctx, "/orama.fees.v1.Query/BaseFee", &feestypes.QueryBaseFeeRequest{}, &bf); err != nil {
		return fail(storageName, "read the base fee: %v", err)
	}
	args := []string{"storage", "create", "--chain-id", e.chainID, "--signer", e.signer.AccountAddress(), "--class", "private",
		"--nonce", fx.nonce, "--replicas", strconv.Itoa(privateReplicas), "--price", dealPricePerEpoch,
		"--duration-epochs", strconv.Itoa(dealDurationEpochs), "--fee", storageTxFee(bf.BaseFee).String(), "--gas", strconv.Itoa(txGas),
		"--node", "http://" + rest}
	for _, s := range specs {
		args = append(args, "--piece", s)
	}
	if out, err = runOrama(ctx, e.orama, agent, args...); err != nil {
		return fail(storageName, "create the deal: %v", err)
	}
	dealID, err := findDealByNonce(ctx, c, fx.nonceBytes)
	if err != nil {
		return fail(storageName, "%v (create said: %s)", err, strings.TrimSpace(out))
	}
	putCtx, cancel := context.WithTimeout(ctx, putWait+time.Minute)
	defer cancel()
	if _, err = runOrama(putCtx, e.orama, nil, "storage", "put", "--deal-id", fmt.Sprint(dealID), "--dir", fx.sealDir,
		"--rpc", "http://"+rpc, "--wait", putWait.String()); err != nil {
		return fail(storageName, "put deal %d: %v", dealID, err)
	}
	slots, err := waitAccepted(ctx, c, dealID)
	if err != nil {
		return fail(storageName, "deal %d: %v", dealID, err)
	}
	if err := distinctProviders(slots); err != nil {
		return fail(storageName, "deal %d: %v", dealID, err)
	}
	if err := waitProofs(ctx, c, dealID, slots); err != nil {
		return fail(storageName, "deal %d: %v", dealID, err)
	}
	if err := roundTrip(ctx, e, fx, rpc, dealID); err != nil {
		return fail(storageName, "deal %d: %v", dealID, err)
	}
	nodes := make([]string, len(slots))
	for i, s := range slots {
		nodes[i] = s.NodeId
	}
	return pass(storageName, "deal %d: %d slots on distinct providers %v, proofs accepted, get and open round trip match", dealID, len(slots), nodes)
}

// storageFixture is the local files of one test deal.
type storageFixture struct {
	keyFile, seedFile, plainFile, sealDir, nonce string
	nonceBytes, plain                            []byte
}

func newStorageFixture(dir string, plainBytes int) (storageFixture, error) {
	fx := storageFixture{
		keyFile: filepath.Join(dir, "storage.key"), seedFile: filepath.Join(dir, "repair.seed"),
		plainFile: filepath.Join(dir, "plain.bin"), sealDir: filepath.Join(dir, "sealed"),
	}
	keyHex, _, err := randomHex(32)
	if err != nil {
		return fx, err
	}
	seedHex, _, err := randomHex(32)
	if err != nil {
		return fx, err
	}
	if fx.nonce, fx.nonceBytes, err = randomHex(32); err != nil {
		return fx, err
	}
	fx.plain = make([]byte, plainBytes)
	if _, err := rand.Read(fx.plain); err != nil {
		return fx, err
	}
	for path, data := range map[string][]byte{fx.keyFile: []byte(keyHex + "\n"), fx.seedFile: []byte(seedHex + "\n"), fx.plainFile: fx.plain} {
		if err := writeSecret(path, data); err != nil {
			return fx, err
		}
	}
	return fx, nil
}

func slotSizes(dir string, n int) (map[int]int64, error) {
	sizes := map[int]int64{}
	for i := 0; i < n; i++ {
		fi, err := os.Stat(filepath.Join(dir, fmt.Sprintf("slot-%d", i)))
		if err != nil {
			return nil, err
		}
		sizes[i] = fi.Size()
	}
	return sizes, nil
}

// waitAccepted waits for every slot of the deal to be assigned and accepted by its provider.
func waitAccepted(ctx context.Context, c *node.Client, dealID uint64) ([]storagetypes.Slot, error) {
	var slots []storagetypes.Slot
	err := waitFor(ctx, acceptWait, func() (bool, error) {
		slots = slots[:0]
		for i := uint32(0); i < privateReplicas; i++ {
			var resp storagetypes.QuerySlotResponse
			if err := c.Query(ctx, "/orama.storage.v1.Query/Slot", &storagetypes.QuerySlotRequest{DealId: dealID, Slot: i}, &resp); err != nil {
				return false, err
			}
			if !resp.Slot.Accepted {
				return false, fmt.Errorf("slot %d (node %q) is not accepted, status %s", i, resp.Slot.NodeId, resp.Slot.Status)
			}
			slots = append(slots, resp.Slot)
		}
		return true, nil
	})
	return slots, err
}

// waitProofs waits until an epoch has closed after acceptance, then judges the deal's challenges.
func waitProofs(ctx context.Context, c *node.Client, dealID uint64, slots []storagetypes.Slot) error {
	first, err := currentEpoch(ctx, c)
	if err != nil {
		return err
	}
	var last error
	err = waitFor(ctx, epochWait, func() (bool, error) {
		cur, err := currentEpoch(ctx, c)
		if err != nil {
			return false, err
		}
		if cur <= first {
			return false, fmt.Errorf("still in epoch %d", cur)
		}
		views, err := dealChallenges(ctx, c, dealID, slots, first, cur)
		if err != nil {
			return false, err
		}
		nodes := make([]string, len(slots))
		for i, s := range slots {
			nodes[i] = s.NodeId
		}
		last = proofsAccepted(nodes, views, cur)
		return last == nil, last
	})
	return err
}

func dealChallenges(ctx context.Context, c *node.Client, dealID uint64, slots []storagetypes.Slot, from, to uint64) ([]challengeView, error) {
	var views []challengeView
	for ep := from; ep <= to; ep++ {
		for _, s := range slots {
			var resp storagetypes.QueryChallengesResponse
			if err := c.Query(ctx, "/orama.storage.v1.Query/Challenges", &storagetypes.QueryChallengesRequest{Epoch: ep, NodeId: s.NodeId}, &resp); err != nil {
				return nil, err
			}
			for _, ch := range resp.Challenges {
				if ch.DealId == dealID {
					views = append(views, challengeView{Epoch: ep, NodeID: s.NodeId, Proved: ch.Proved})
				}
			}
		}
	}
	return views, nil
}

// roundTrip fetches the deal back with `orama storage get` and opens one sealed slot with `orama
// storage open`; both must give the plaintext that was sealed.
func roundTrip(ctx context.Context, e *env, fx storageFixture, rpc string, dealID uint64) error {
	got := filepath.Join(filepath.Dir(fx.sealDir), "got.bin")
	if _, err := runOrama(ctx, e.orama, nil, "storage", "get", "--deal-id", fmt.Sprint(dealID), "--rpc", "http://"+rpc,
		"--storage-key-file", fx.keyFile, "--repair-seed-file", fx.seedFile, "--out", got); err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if err := sameBytes(got, fx.plain); err != nil {
		return fmt.Errorf("get: %w", err)
	}
	opened := filepath.Join(filepath.Dir(fx.sealDir), "opened.bin")
	if _, err := runOrama(ctx, e.orama, nil, "storage", "open", "--storage-key-file", fx.keyFile, "--repair-seed-file", fx.seedFile,
		"--nonce", fx.nonce, "--slot", "0", "--in", filepath.Join(fx.sealDir, "slot-0"), "--out", opened); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if err := sameBytes(opened, fx.plain); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	return nil
}

func sameBytes(path string, want []byte) error {
	have, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(have, want) {
		return fmt.Errorf("plaintext differs: sha256 %s, want %s", sha(have), sha(want))
	}
	return nil
}
