package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cast"

	"github.com/cosmos/cosmos-sdk/client/flags"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	orchardverify "github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchardproc"
)

const (
	// FlagShieldedVerifier names the out-of-process verifier binary, the second verifier of
	// x/shielded. It is a flag of `oramad start` and a key of the app options.
	FlagShieldedVerifier = "shielded-verifier"

	// removedPinOption was a setting for the pin until the pin became link-time only. A node that
	// still has it set is told it is ignored.
	removedPinOption = "shielded-verifier-sha256"

	// ShieldedVerifierBinary is the file name the release ships, and the default under
	// <home>/bin/ when the flag is not given.
	ShieldedVerifierBinary = "orama-orchard-verifier"
)

// verifiersOptional reports whether a node of chainID may start without the shielded verifiers:
// localnets and the devnet a script builds for one e2e run (e2e/scripts/chain-deploy.sh), both
// built without the orchard library. A node of any other chain (stagenet, testnet, mainnet) that
// cannot verify votes on proposals it never checked and forks itself off at the first shielded
// transaction it executes, so it refuses to start instead.
func verifiersOptional(chainID string) bool {
	class := params.ClassOfChainID(chainID)
	return class == params.ChainLocalnet || class == params.ChainDevnet
}

// CheckNodeCanVerify is `oramad start`'s refusal to run a node of chainID that cannot verify
// shielded bundles: one built without the orchard library, or with no verifier binary at the path
// appOpts names. Localnets and scripted devnets are exempt (verifiersOptional).
func CheckNodeCanVerify(chainID string, appOpts servertypes.AppOptions) error {
	if verifiersOptional(chainID) {
		return nil
	}
	path := shieldedVerifierPath(appOpts)
	present := regularFile(path)
	if orchardverify.Linked && present {
		if !orchardproc.StartsFromCheckedFile {
			return fmt.Errorf("a node of chain %q must start its shielded verifier from the file whose hash it checked, which only Linux can do (this host is %s): "+
				"run oramad on Linux; only a localnet or a scripted devnet may run elsewhere", chainID, runtime.GOOS)
		}
		return nil
	}
	return fmt.Errorf("a node of chain %q must verify shielded bundles and this one cannot "+
		"(orchard library linked: %t; verifier binary %q present: %t): install the release's %s at that "+
		"path or pass --%s, and run an oramad built with the orchard library",
		chainID, orchardverify.Linked, path, present, ShieldedVerifierBinary, FlagShieldedVerifier)
}

// regularFile reports a regular file at path, not followed through a link at the file itself: the
// same rule the pin check applies when it opens the binary (orchardproc openPinned).
func regularFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// ShieldedVerifierSHA256 is the hex SHA-256 of the verifier binary this oramad was built for. A
// release sets it at link time (-ldflags "-X .../app.ShieldedVerifierSHA256=<hex>"). It is the only
// source of the pin: there is no flag, config key or environment variable that sets it, because a
// pin an operator can choose lets two nodes run different verifiers and reach different verdicts on
// the same bundle. Changing the pinned binary means changing oramad, which is a chain upgrade. Empty
// means this build can start no verifier binary (a localnet or devnet build; a build for any other
// chain refuses to start, CheckNodeCanVerify). Tests set it directly.
var ShieldedVerifierSHA256 = ""

// shieldedVerifierPath is where the node looks for the out-of-process verifier: the flag, else
// <home>/bin/orama-orchard-verifier.
func shieldedVerifierPath(appOpts servertypes.AppOptions) string {
	if path := cast.ToString(appOpts.Get(FlagShieldedVerifier)); path != "" {
		return path
	}
	home := cast.ToString(appOpts.Get(flags.FlagHome))
	if home == "" {
		return ""
	}
	return filepath.Join(home, "bin", ShieldedVerifierBinary)
}

// FileSHA256 is the hex SHA-256 of a file, the value to link into oramad as ShieldedVerifierSHA256.
func FileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// newChildVerifiers builds the two verifier sets that are not the block's. Each is a second
// out-of-process verifier over the same pinned binary, with a child process of its own and a short
// request timeout (mempoolRequestTimeout), and then the same linked library (stateless, safe for
// concurrent use). The mempool set serves CheckTx, ReCheck and gRPC simulations, which anyone can
// send; the proposal set serves ProcessProposal and PrepareProposal and is never touched by the
// mempool's work, so a flood of CheckTx or simulations cannot make a proposal wait or fail. Neither
// shares the block's verifier mutex, and a bundle that hangs one of these children stalls it for at
// most that timeout. They do not isolate consensus from the mempool: CometBFT's local ABCI client
// takes one lock for CheckTx and FinalizeBlock, so a CheckTx waiting on its child delays a block by
// the same bounded time. Each child is started at construction in the background, and again in the
// background after a fault or when found gone (the readiness check starts it, at most once per
// back-off), so none builds the Halo 2 key under that lock: until it is ready the answer is
// verify.ErrVerifierStarting, which is neither a verdict nor a fault. The child goes first in a set
// because it can be killed and replaced: a bundle that makes it fault is stopped there. A bundle
// that aborts or hangs the in-process library has nothing in front of it, and still stops the node
// (see the shielded chapter).
func (app *OramaApp) newChildVerifiers(block []verify.Verifier, appOpts servertypes.AppOptions) (mempool, proposal []verify.Verifier) {
	if len(block) != verify.MinVerifiers {
		return nil, nil
	}
	path := shieldedVerifierPath(appOpts)
	build := func(role string) []verify.Verifier {
		process, err := orchardproc.New(orchardproc.Config{
			Path: path, ChainID: app.ChainID(), SHA256: ShieldedVerifierSHA256, RequestTimeout: mempoolRequestTimeout,
		})
		if err != nil {
			panic(fmt.Errorf("failed to build the %s out-of-process verifier for chain %q: %w", role, app.ChainID(), err))
		}
		m := newMempoolVerifier(process, app.Logger().With("module", "x/shielded"))
		m.role = role
		if regularFile(path) || cast.ToString(appOpts.Get(FlagShieldedVerifier)) != "" {
			m.configured = true
			m.warm()
		}
		return []verify.Verifier{m, block[0]}
	}
	return build("mempool"), build("proposal")
}

// newShieldedVerifiers builds the two verifiers a shielded bundle must pass: the orchard library
// linked through cgo and the pinned binary run out of process. Both are bound to the chain id.
//
// An app with no chain id is not a node: it is the throwaway instance the CLI builds to read module
// metadata. It gets no verifiers, so verify.Check refuses every bundle, and it starts nothing.
//
// On a node, both verifiers are warmed here, at construction, so their verifying keys are not built
// inside a consensus handler. When the library is linked the node must have a working process
// verifier as well, and a failure to warm either stops the app: a node that cannot verify must not
// serve blocks. A build without the library cannot accept a bundle, so it warms nothing and says so;
// `oramad start` refuses to run such a node on any chain but a localnet or a scripted devnet
// (CheckNodeCanVerify).
func (app *OramaApp) newShieldedVerifiers(appOpts servertypes.AppOptions) []verify.Verifier {
	chainID := app.ChainID()
	if chainID == "" {
		return nil
	}
	library, err := orchardverify.New(chainID)
	if err != nil {
		panic(fmt.Errorf("failed to build the Orchard verifier for chain %q: %w", chainID, err))
	}
	path, pin := shieldedVerifierPath(appOpts), ShieldedVerifierSHA256
	if stray := cast.ToString(appOpts.Get(removedPinOption)); stray != "" {
		app.Logger().Error("ignoring "+removedPinOption+": the verifier pin is linked into oramad and no setting changes it; remove it from app.toml and the environment", "module", "x/shielded")
	}
	process, err := orchardproc.New(orchardproc.Config{Path: path, ChainID: chainID, SHA256: pin})
	if err != nil {
		panic(fmt.Errorf("failed to build the out-of-process verifier for chain %q: %w", chainID, err))
	}
	logger := app.Logger().With("module", "x/shielded")
	present := regularFile(path)
	logger.Info("shielded verifiers", "library_linked", orchardverify.Linked,
		"process_binary", path, "process_binary_present", present, "process_binary_pinned", pin != "")
	if orchardverify.Linked {
		if err := orchardverify.Warm(); err != nil {
			panic(fmt.Errorf("failed to build the Orchard verifying key: %w", err))
		}
	}
	// The process verifier is warmed when the operator configured it (the flag or a binary at the
	// default path). A configured verifier that will not start stops the node.
	if present || cast.ToString(appOpts.Get(FlagShieldedVerifier)) != "" {
		if err := process.Warm(); err != nil {
			panic(fmt.Errorf("the out-of-process shielded verifier %q will not start (check --%s: the file must be the release's %s that this oramad pins): %w",
				path, FlagShieldedVerifier, ShieldedVerifierBinary, err))
		}
	}
	if !orchardverify.Linked || !present {
		logger.Warn("this node accepts no shielded bundle: it needs the orchard library linked (an orchardffi build) and the verifier binary",
			"flag", "--"+FlagShieldedVerifier)
	}
	return []verify.Verifier{library, process}
}
