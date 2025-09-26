package encryption

import (
	"crypto/rand"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

type IdentityInfo struct {
	PrivateKey crypto.PrivKey
	PublicKey  crypto.PubKey
	PeerID     peer.ID
}

func GenerateIdentity() (*IdentityInfo, error) {
	priv, pub, err := crypto.GenerateKeyPairWithReader(crypto.Ed25519, 2048, rand.Reader)
	if err != nil {
		return nil, err
	}

	peerID, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return nil, err
	}

	return &IdentityInfo{
		PrivateKey: priv,
		PublicKey:  pub,
		PeerID:     peerID,
	}, nil
}

func SaveIdentity(identity *IdentityInfo, path string) error {
	data, err := crypto.MarshalPrivateKey(identity.PrivateKey)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func LoadIdentity(path string) (*IdentityInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	priv, err := crypto.UnmarshalPrivateKey(data)
	if err != nil {
		return nil, err
	}

	pub := priv.GetPublic()
	peerID, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return nil, err
	}

	return &IdentityInfo{
		PrivateKey: priv,
		PublicKey:  pub,
		PeerID:     peerID,
	}, nil
}
