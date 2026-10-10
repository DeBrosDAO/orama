// Package chainfaucet is the public faucet of a test network: a gateway that holds a faucet key
// signs MsgFaucet (x/emission) for whoever asks, so a newcomer with no account, no node and no
// SSH access can be funded.
//
// The chain does the policing. MsgFaucet is refused on a production chain id, unless the genesis
// enabled the faucet, above the maximum drip, inside a recipient's cooldown, past the epoch's cap
// and for a module or blocked account; every transaction is simulated before it is signed, so a
// refusal costs the faucet no fee. What this package adds is the part the chain cannot do for
// itself: it holds the key (key.go), refuses to sign anywhere but a test network (network.go),
// gives the transactions of one account a single order so their sequence numbers cannot collide
// (service.go), and turns the chain's refusals into typed ones a client can act on (refusal.go).
//
// The HTTP route is in pkg/gateway/handlers/chainread (faucet.go); `orama maint faucet init`
// creates the key on a node.
package chainfaucet
