package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cast"

	"github.com/cosmos/cosmos-sdk/client/flags"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	orchardverify "github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchardproc"
)

const (
	// FlagShieldedVerifier names the out-of-process verifier binary, the second verifier of
	// x/shielded. It is a flag of `oramad start` and a key of the app options.
	FlagShieldedVerifier = "shielded-verifier"
	// FlagShieldedVerifierSHA256 is the hex SHA-256 the verifier binary must have. The node refuses
	// to start a binary with any other hash, so two nodes cannot reach different verdicts because
	// one runs a different file.
	FlagShieldedVerifierSHA256 = "shielded-verifier-sha256"

	// ShieldedVerifierBinary is the file name the release ships, and the default under
	// <home>/bin/ when the flag is not given.
	ShieldedVerifierBinary = "orama-orchard-verifier"
)

// ShieldedVerifierSHA256 is the hex SHA-256 of the verifier binary this oramad was built for. A
// release sets it at link time (-ldflags "-X .../app.ShieldedVerifierSHA256=<hex>"); the flag
// overrides it. Empty means the operator must give the flag.
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

func shieldedVerifierPin(appOpts servertypes.AppOptions) string {
	if pin := cast.ToString(appOpts.Get(FlagShieldedVerifierSHA256)); pin != "" {
		return pin
	}
	return ShieldedVerifierSHA256
}

// FileSHA256 is the hex SHA-256 of a file, the value to give --shielded-verifier-sha256.
func FileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
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
// serve blocks. A build without the library cannot accept a bundle, so it warms nothing and says so.
func (app *OramaApp) newShieldedVerifiers(appOpts servertypes.AppOptions) []verify.Verifier {
	chainID := app.ChainID()
	if chainID == "" {
		return nil
	}
	library, err := orchardverify.New(chainID)
	if err != nil {
		panic(fmt.Errorf("failed to build the Orchard verifier for chain %q: %w", chainID, err))
	}
	path, pin := shieldedVerifierPath(appOpts), shieldedVerifierPin(appOpts)
	process, err := orchardproc.New(orchardproc.Config{Path: path, ChainID: chainID, SHA256: pin})
	if err != nil {
		panic(fmt.Errorf("failed to build the out-of-process verifier for chain %q: %w", chainID, err))
	}
	logger := app.Logger().With("module", "x/shielded")
	_, statErr := os.Stat(path)
	present := path != "" && statErr == nil
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
			panic(fmt.Errorf("the out-of-process shielded verifier %q will not start (check --%s and --%s): %w",
				path, FlagShieldedVerifier, FlagShieldedVerifierSHA256, err))
		}
	}
	if !orchardverify.Linked || !present {
		logger.Warn("this node accepts no shielded bundle: it needs the orchard library linked (an orchardffi build) and the verifier binary",
			"flag", "--"+FlagShieldedVerifier)
	}
	return []verify.Verifier{library, process}
}
