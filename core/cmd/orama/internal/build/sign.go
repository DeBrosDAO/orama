package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// signingChain is the only chain the RootWallet agent signs for, and the one
// nodes recover archive signers on.
const signingChain = "evm"

// addressLookupTimeout bounds asking the agent for its address, which never
// prompts.
const addressLookupTimeout = 10 * time.Second

// signTimeout covers the agent's approval prompt on a first signature
// (wallet:sign), plus the margin the rest of the CLI gives it.
const signTimeout = rwagent.AgentApprovalTimeout + 30*time.Second

// archiveSigner is the part of the RootWallet agent a build signs with.
// *rwagent.Client is one; a test serves the same socket protocol.
type archiveSigner interface {
	GetAddress(ctx context.Context, chain string) (*rwagent.WalletAddressData, error)
	SignForPurpose(ctx context.Context, message, chain, purpose string) (*rwagent.WalletSignData, error)
}

// newAgentSigner is the RootWallet agent `orama node setup` also talks to.
func newAgentSigner() archiveSigner {
	return rwagent.New(os.Getenv("RW_AGENT_SOCK"))
}

// signerAddress asks the agent which account will sign. A build checks this
// before compiling anything, so a locked or absent wallet fails in seconds
// rather than after the whole build.
func signerAddress(agent archiveSigner) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), addressLookupTimeout)
	defer cancel()
	data, err := agent.GetAddress(ctx, signingChain)
	if err != nil {
		return "", fmt.Errorf("the archive is signed with your RootWallet, and its agent did not answer: %w "+
			"(start and unlock the RootWallet desktop app, or pass --unsigned for a local-only archive "+
			"that no node will install)", err)
	}
	if data.Address == "" {
		return "", fmt.Errorf("the RootWallet agent reported no active account to sign the archive with")
	}
	return strings.ToLower(data.Address), nil
}

// sealManifest renders the manifest exactly as it goes into the archive and,
// when signer is set, signs those bytes through the agent. The signature is
// checked with the verifier every node runs (archivetrust.RecoverSigner)
// before the archive is written: a signature that would not verify on a node,
// or that verifies to a different account than the agent reported, fails the
// build instead of every install.
func sealManifest(m *Manifest, agent archiveSigner, signer string) (manifestJSON []byte, signature string, err error) {
	manifestJSON, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("render the manifest: %w", err)
	}
	if signer == "" {
		return manifestJSON, "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), signTimeout)
	defer cancel()
	message, err := archivetrust.SigningMessage(manifestJSON)
	if err != nil {
		return nil, "", err
	}
	// The archive purpose: the agent signs this format only under it and only
	// for a caller holding wallet:sign:orama-archive, so a login challenge or
	// any other plain signing request can never yield a build signature.
	data, err := agent.SignForPurpose(ctx, message, signingChain, rwagent.PurposeOramaArchive)
	if err != nil {
		return nil, "", fmt.Errorf("sign the manifest with your RootWallet: %w", err)
	}
	recovered, err := archivetrust.RecoverSigner(manifestJSON, data.Signature)
	if err != nil {
		return nil, "", fmt.Errorf("the RootWallet agent returned a signature nodes cannot verify: %w", err)
	}
	if recovered != signer {
		return nil, "", fmt.Errorf("the RootWallet agent signed as %s but reports %s as its account; "+
			"nodes would attribute this build to %s", recovered, signer, recovered)
	}
	return manifestJSON, data.Signature, nil
}

// signingPlan checks the signing flags before anything is compiled and
// returns the signer's address, or "" for an unsigned build.
func (b *Builder) signingPlan() (string, error) {
	if b.flags.Unsigned {
		if len(b.flags.Signers) > 0 {
			return "", fmt.Errorf("--signers rotates the signers nodes trust, which only a signed archive can do; drop --unsigned")
		}
		return "", nil
	}
	signer, err := signerAddress(b.agent)
	if err != nil {
		return "", err
	}
	if len(b.flags.Signers) > 0 {
		signers, err := archivetrust.NormalizeSigners(b.flags.Signers)
		if err != nil {
			return "", fmt.Errorf("--signers: %w", err)
		}
		// Nodes refuse a rotation that leaves out its own signer (the archive
		// must still verify after they rotate), so say so before building.
		if !slices.Contains(signers, signer) {
			return "", fmt.Errorf("--signers must include %s, the account signing this build: nodes refuse a "+
				"rotation that drops its own signer. Retire a key in two builds — this one adds the new key, "+
				"the next, signed by the new key, drops this one", signer)
		}
		b.flags.Signers = signers
	}
	return signer, nil
}
