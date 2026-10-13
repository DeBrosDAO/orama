package app_test

import (
	"crypto"
	stded25519 "crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is SHA-1 by definition
	"crypto/sha256"
	"crypto/x509"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	relaykeeper "github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// relayNode is a registered x/nodes node carrying an ed25519 "relay" binding.
type relayNode struct {
	id       string
	operator sdk.AccAddress
	priv     stded25519.PrivateKey
}

// addRelayNode registers an operator and a RELAY node with the given endpoint on the real app.
func (c *wiringChain) addRelayNode(id, endpoint string) relayNode {
	c.t.Helper()
	return c.addNodeWith(id, endpoint, []nodestypes.Role{nodestypes.RoleRelay}, "relay")
}

// addNodeWith registers an operator and a node with the given roles, whose ed25519 key is bound under service.
func (c *wiringChain) addNodeWith(id, endpoint string, roles []nodestypes.Role, service string) relayNode {
	c.t.Helper()
	n := relayNode{id: id, operator: sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())}
	hotPriv := secp256k1.GenPrivKey()
	hot := sdk.AccAddress(hotPriv.PubKey().Address())
	hotSig, err := hotPriv.Sign(nodestypes.BindingSignBytes(testChainID, n.operator.String(), nodestypes.HotKeyService, hotPriv.PubKey().Bytes()))
	require.NoError(c.t, err)
	pub, priv, err := stded25519.GenerateKey(nil)
	require.NoError(c.t, err)
	n.priv = priv
	c.write(func(ctx sdk.Context) {
		funds := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(params.NoramaPerOrama).MulRaw(20)))
		require.NoError(c.t, mintNoramaForTest(c.app, ctx, funds))
		require.NoError(c.t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, n.operator, funds))
		srv := nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper)
		_, err := srv.RegisterOperator(ctx, &nodestypes.MsgRegisterOperator{Operator: n.operator.String()})
		require.NoError(c.t, err)
		_, err = srv.RegisterNode(ctx, &nodestypes.MsgRegisterNode{
			Operator: n.operator.String(), NodeId: id, Roles: roles, HotKey: hot.String(),
			Bindings: []nodestypes.Binding{{
				Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: hotPriv.PubKey().Bytes(), Signature: hotSig,
			}, {
				Service: service, KeyType: nodestypes.KeyTypeEd25519, Pubkey: pub,
				Signature: stded25519.Sign(priv, nodestypes.BindingSignBytes(testChainID, n.operator.String(), service, pub)),
			}},
			Endpoints: []string{endpoint},
		})
		require.NoError(c.t, err)
	})
	return n
}

// appRSA is a relay's RSA identity: its key, the PKCS#1 DER of the public half and the SHA-1 of that DER,
// which is the fingerprint. One is made per mark for the whole test binary: a key costs milliseconds.
type appRSA struct {
	key *rsa.PrivateKey
	der []byte
	fp  []byte
}

var (
	appRSAs   = map[byte]appRSA{}
	appRSAsMu sync.Mutex
)

func newAppRSA(t testing.TB, mark byte) appRSA {
	t.Helper()
	appRSAsMu.Lock()
	defer appRSAsMu.Unlock()
	if id, ok := appRSAs[mark]; ok {
		return id
	}
	key, err := rsa.GenerateKey(rand.Reader, relaytypes.MinRSABits)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	sum := sha1.Sum(der)
	id := appRSA{key: key, der: der, fp: sum[:]}
	appRSAs[mark] = id
	return id
}

// proof is the RSA identity's signature over the proof message of a node and its relay key.
func (r appRSA) proof(t testing.TB, nodeID string, ed25519Pub []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(relaytypes.RSAProofMessage(nodeID, ed25519Pub, false))
	sig, err := rsa.SignPKCS1v15(rand.Reader, r.key, crypto.SHA256, sum[:])
	require.NoError(t, err)
	return sig
}

// registerMsg is a MsgRegisterRelay for n with the RSA identity id, signed by n's relay key and by id.
func (c *wiringChain) registerMsg(n relayNode, id appRSA) *relaytypes.MsgRegisterRelay {
	c.t.Helper()
	return &relaytypes.MsgRegisterRelay{
		Operator: n.operator.String(), NodeId: n.id, RsaFingerprint: id.fp,
		Ed25519Signature: stded25519.Sign(n.priv, relaytypes.CrossCertMessage(n.id, id.fp, false)),
		RsaPublicKey:     id.der,
		RsaSignature:     id.proof(c.t, n.id, n.priv.Public().(stded25519.PublicKey)),
	}
}

// registerRelay sends MsgRegisterRelay through x/relay's msg server, wired to the real x/nodes. The
// relay's RSA identity is the one called mark; it returns the fingerprint.
func (c *wiringChain) registerRelay(n relayNode, mark byte) []byte {
	c.t.Helper()
	id := newAppRSA(c.t, mark)
	c.write(func(ctx sdk.Context) {
		_, err := relaykeeper.NewMsgServerImpl(c.app.RelayKeeper).RegisterRelay(ctx, c.registerMsg(n, id))
		require.NoError(c.t, err)
	})
	return id.fp
}

func (c *wiringChain) storedRelay(fp []byte) relaytypes.Relay {
	c.t.Helper()
	relay, err := c.app.RelayKeeper.Relays.Get(c.app.NewContext(true), fp)
	require.NoError(c.t, err)
	return relay
}

func TestRegisterRelay_literalIPEndpointGetsItsSlash16(t *testing.T) {
	c := newWiringChain(t)
	n := c.addRelayNode("relay-ip", "https://93.184.216.34:443")
	fp := c.registerRelay(n, 0x11)
	require.Equal(t, "93.184.0.0/16", c.storedRelay(fp).Prefix16)
}

func TestRegisterRelay_noLiteralIPUsesUnidentifiedBucket(t *testing.T) {
	c := newWiringChain(t)
	a := c.addRelayNode("relay-host-a", "https://relay-a.example.com:443")
	b := c.addRelayNode("relay-host-b", "https://relay-b.example.com:443")
	fpA := c.registerRelay(a, 0x11)
	fpB := c.registerRelay(b, 0x22)
	require.Equal(t, relaytypes.UnidentifiedPrefix16, c.storedRelay(fpA).Prefix16)
	require.Equal(t, relaytypes.UnidentifiedPrefix16, c.storedRelay(fpB).Prefix16)
}

func TestRegisterRelay_identityInsideLockIsUnidentified(t *testing.T) {
	c := newWiringChainWith(t, func(gs *nodestypes.GenesisState) {
		gs.Params.NetworkIdentityLockSeconds = 1_000_000
	})
	n := c.addRelayNode("relay-new", "https://93.184.216.34:443")
	fp := c.registerRelay(n, 0x11)
	require.Equal(t, relaytypes.UnidentifiedPrefix16, c.storedRelay(fp).Prefix16)
}

// registerRelayErr sends MsgRegisterRelay for n with the RSA identity called mark through x/relay wired to
// the real x/nodes.
func (c *wiringChain) registerRelayErr(n relayNode, mark byte) error {
	c.t.Helper()
	var err error
	c.write(func(ctx sdk.Context) {
		_, err = relaykeeper.NewMsgServerImpl(c.app.RelayKeeper).RegisterRelay(ctx, c.registerMsg(n, newAppRSA(c.t, mark)))
	})
	return err
}

func TestRegisterRelay_refusesANodeWithoutTheRelayRole(t *testing.T) {
	c := newWiringChain(t)
	n := c.addNodeWith("storage-only", "https://93.184.216.34:443", []nodestypes.Role{nodestypes.RoleStorage}, "relay")
	err := c.registerRelayErr(n, 0x11)
	require.ErrorContains(t, err, "does not have role ROLE_RELAY")
}

func TestRegisterRelay_refusesARelayNodeThatBindsNoRelayKey(t *testing.T) {
	c := newWiringChain(t)
	n := c.addNodeWith("relay-consensus", "https://93.184.216.34:443", []nodestypes.Role{nodestypes.RoleRelay}, "consensus")
	err := c.registerRelayErr(n, 0x11)
	require.ErrorContains(t, err, `binds no ed25519 key for service "relay"`,
		"another ed25519 key of the node is never taken for the relay's: its votes would never match")
}

func TestUpdateRelay_followsAMsgUpdateNodeRebind(t *testing.T) {
	c := newWiringChain(t)
	n := c.addRelayNode("relay-rotate", "https://93.184.216.34:443")
	fp := c.registerRelay(n, 0x11)

	newPub, newPriv, err := stded25519.GenerateKey(nil)
	require.NoError(t, err)
	hot := c.nodeBindings(n.id)[0]
	c.write(func(ctx sdk.Context) {
		_, err := nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).UpdateNode(ctx, &nodestypes.MsgUpdateNode{
			Operator: n.operator.String(), NodeId: n.id,
			Bindings: []nodestypes.Binding{hot, {
				Service: "relay", KeyType: nodestypes.KeyTypeEd25519, Pubkey: newPub,
				Signature: stded25519.Sign(newPriv, nodestypes.BindingSignBytes(testChainID, n.operator.String(), "relay", newPub)),
			}},
		})
		require.NoError(t, err)
	})
	require.Equal(t, []byte(n.priv.Public().(stded25519.PublicKey)), c.storedRelay(fp).Ed25519Id, "registration snapshotted the old key")

	c.write(func(ctx sdk.Context) {
		id := newAppRSA(t, 0x11)
		_, err := relaykeeper.NewMsgServerImpl(c.app.RelayKeeper).UpdateRelay(ctx, &relaytypes.MsgUpdateRelay{
			Operator: n.operator.String(), NodeId: n.id, RsaFingerprint: fp,
			Ed25519Signature: stded25519.Sign(newPriv, relaytypes.CrossCertMessage(n.id, fp, false)),
			RsaPublicKey:     id.der,
			RsaSignature:     id.proof(t, n.id, newPub),
		})
		require.NoError(t, err)
	})
	require.Equal(t, []byte(newPub), c.storedRelay(fp).Ed25519Id)
}

// nodeBindings is the binding set the chain holds for a node.
func (c *wiringChain) nodeBindings(id string) []nodestypes.Binding {
	c.t.Helper()
	node, err := c.app.NodesKeeper.GetNode(c.app.NewContext(true), id)
	require.NoError(c.t, err)
	return node.Bindings
}
