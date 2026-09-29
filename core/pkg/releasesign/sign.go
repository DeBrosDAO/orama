// Package releasesign produces TUF release signatures through the RootWallet
// agent's release purpose (wallet:sign:orama-release).
//
// The release key is a dedicated ed25519 key that only the agent holds. It is
// not the account key that signs build archives, and the agent signs nothing
// for it except a TUF "signed" section in canonical JSON. That payload opens
// with {"_type":", so no plain signing request and no archive message can
// yield a release signature, and no release payload can yield an archive one.
//
// This package builds the payload, asks the agent, and checks the answer with
// the same ed25519 verification a node's TUF client runs before attaching it.
package releasesign

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/secure-systems-lab/go-securesystemslib/cjson"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// SigningChain is the chain value sent with a release request. The agent
// signs a release payload with its release key whatever the account chain.
const SigningChain = "ed25519"

// Signer is the part of the RootWallet agent a release is signed with.
// *rwagent.Client is one; a test serves the same contract with a local key.
type Signer interface {
	SignForPurpose(ctx context.Context, message, chain, purpose string) (*rwagent.WalletSignData, error)
}

// ErrBadSignature is an agent answer that is not a valid signature of the
// payload by the expected release key.
var ErrBadSignature = errors.New("the agent's release signature does not verify")

// Payload is the exact byte string TUF signs for meta: the canonical JSON of
// its "signed" section.
func Payload[T metadata.Roles](meta *metadata.Metadata[T]) ([]byte, error) {
	payload, err := cjson.EncodeCanonical(meta.Signed)
	if err != nil {
		return nil, fmt.Errorf("encode the signed section: %w", err)
	}
	if !strings.HasPrefix(string(payload), rwagent.ReleasePayloadPrefix) {
		return nil, fmt.Errorf("the signed section does not open with %s", rwagent.ReleasePayloadPrefix)
	}
	return payload, nil
}

// Sign asks the agent to sign meta's payload under the release purpose and
// appends the signature to meta. pub is the release key the TUF root lists for
// the role; a signature that does not verify under it is refused, so a wrong
// key or a tampered answer never reaches published metadata.
func Sign[T metadata.Roles](ctx context.Context, agent Signer, meta *metadata.Metadata[T], pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("release public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
	payload, err := Payload(meta)
	if err != nil {
		return err
	}
	data, err := agent.SignForPurpose(ctx, string(payload), SigningChain, rwagent.PurposeOramaRelease)
	if err != nil {
		return fmt.Errorf("sign the release metadata with your RootWallet: %w", err)
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(data.Signature, "0x"))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: not a %d-byte hex signature", ErrBadSignature, ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return ErrBadSignature
	}
	key, err := metadata.KeyFromPublicKey(pub)
	if err != nil {
		return fmt.Errorf("release key: %w", err)
	}
	keyID, err := key.ID()
	if err != nil {
		return fmt.Errorf("release key id: %w", err)
	}
	meta.Signatures = append(meta.Signatures, metadata.Signature{KeyID: keyID, Signature: sig})
	return nil
}
