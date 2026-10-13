// Package shielded produces the shielded bundles of a load run and keeps them between runs.
// Proving is slow and runs in Rust (chain/x/shielded/wallet, the gen_bench example), so bundles are
// built before the measured part of a run and cached on disk, bound to the chain and the heights
// they are valid for.
package shielded

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

// WalletCrate is where the bundle factory lives, relative to the chain module root.
const WalletCrate = "x/shielded/wallet"

// Factory request modes, the "mode" field of the JSON the factory reads.
const (
	modeShields = "shields"
	modeSpends  = "spends"
)

// OutputSpec is one note to create: for the wallet account's address at the given index.
type OutputSpec struct {
	Account uint32 `json:"account"`
	Address uint32 `json:"address"`
	Value   uint64 `json:"value"`
}

// ShieldJob is one bundle that puts value into the pool.
type ShieldJob struct {
	ID      string       `json:"id"`
	Outputs []OutputSpec `json:"outputs"`
}

// SpendRef is a note to spend and the wallet account that owns it.
type SpendRef struct {
	Account uint32 `json:"account"`
	Note    string `json:"note"`
}

// SpendJob is one transfer or unshield.
type SpendJob struct {
	ID             string       `json:"id"`
	Kind           string       `json:"kind"`
	Spends         []SpendRef   `json:"spends"`
	Outputs        []OutputSpec `json:"outputs"`
	UnshieldSigner string       `json:"unshield_signer,omitempty"`
}

// Job kinds a SpendJob takes.
const (
	KindTransfer = "transfer"
	KindUnshield = "unshield"
)

// NoteOut is a note the factory found in a bundle it built: serialized, so a later request can
// spend it.
type NoteOut struct {
	Account uint32 `json:"account"`
	Value   uint64 `json:"value"`
	Action  int    `json:"action"`
	Cmx     string `json:"cmx"`
	Note    string `json:"note"`
}

// Built is one proved bundle.
type Built struct {
	ID           string    `json:"id"`
	Bundle       string    `json:"bundle"`
	Actions      int       `json:"actions"`
	ValueBalance int64     `json:"value_balance"`
	Anchor       string    `json:"anchor"`
	Nullifiers   []string  `json:"nullifiers"`
	Cmxs         []string  `json:"cmxs"`
	Notes        []NoteOut `json:"notes"`
}

// Response is the factory's answer.
type Response struct {
	Built  []Built `json:"built"`
	Anchor string  `json:"anchor"`
}

type shieldsRequest struct {
	Mode    string      `json:"mode"`
	ChainID string      `json:"chain_id"`
	Jobs    []ShieldJob `json:"jobs"`
}

type spendsRequest struct {
	Mode       string     `json:"mode"`
	ChainID    string     `json:"chain_id"`
	Leaves     []string   `json:"leaves"`
	ExpectRoot string     `json:"expect_root,omitempty"`
	Jobs       []SpendJob `json:"jobs"`
}

// Factory runs the Rust bundle factory.
type Factory struct {
	// Dir is the wallet crate directory the factory runs in.
	Dir string
	// Binary, when set, is a built gen_bench to run instead of `cargo run`.
	Binary string
	// Progress receives the factory's stderr, one line per proved bundle.
	Progress io.Writer
}

// Shields proves one bundle per job. The bundles spend nothing real and are anchored to the empty
// tree, so they stay valid whatever the chain holds.
func (f Factory) Shields(ctx context.Context, chainID string, jobs []ShieldJob) ([]Built, error) {
	resp, err := f.run(ctx, shieldsRequest{Mode: modeShields, ChainID: chainID, Jobs: jobs})
	if err != nil {
		return nil, err
	}
	return resp.Built, checkCount(len(jobs), len(resp.Built))
}

// Spends proves one bundle per job against the tree whose commitments are leaves, in tree order.
// expectRoot is the chain's tree root: the factory refuses leaves that do not root to it.
func (f Factory) Spends(ctx context.Context, chainID string, leaves []string, expectRoot string, jobs []SpendJob) (Response, error) {
	resp, err := f.run(ctx, spendsRequest{Mode: modeSpends, ChainID: chainID, Leaves: leaves, ExpectRoot: expectRoot, Jobs: jobs})
	if err != nil {
		return Response{}, err
	}
	return resp, checkCount(len(jobs), len(resp.Built))
}

func checkCount(want, got int) error {
	if want != got {
		return fmt.Errorf("the factory built %d bundles for %d jobs", got, want)
	}
	return nil
}

func (f Factory) command(ctx context.Context) *exec.Cmd {
	if f.Binary != "" {
		return exec.CommandContext(ctx, f.Binary)
	}
	cmd := exec.CommandContext(ctx, "cargo", "run", "--release", "--locked", "--quiet", "--example", "gen_bench")
	cmd.Dir = f.Dir
	return cmd
}

func (f Factory) run(ctx context.Context, req any) (Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode the factory request: %w", err)
	}
	cmd := f.command(ctx)
	var out, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if f.Progress != nil {
		cmd.Stderr = io.MultiWriter(&stderr, f.Progress)
	}
	if err := cmd.Run(); err != nil {
		return Response{}, fmt.Errorf("run the bundle factory in %s (needs the Rust toolchain): %w: %s", f.Dir, err, lastLines(stderr.String()))
	}
	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("decode the factory response: %w", err)
	}
	return resp, nil
}

// lastLines keeps the tail of a failed command's stderr for an error message.
func lastLines(s string) string {
	const keep = 600
	if len(s) <= keep {
		return s
	}
	return "..." + s[len(s)-keep:]
}
