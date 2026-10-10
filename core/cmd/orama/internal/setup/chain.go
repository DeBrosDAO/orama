package setup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

const (
	// chainReadTimeout bounds one read of the chain's REST API.
	chainReadTimeout = 15 * time.Second
	// chainReadLimit bounds an answer.
	chainReadLimit = 4 << 20
	// denom is the chain's base denomination.
	denom = "norama"
	// chainBodyShown is how much of a refused answer an error carries.
	chainBodyShown = 200
	// roleNamePrefix is how the REST gateway names an x/nodes role in JSON.
	roleNamePrefix = "ROLE_"
)

// tunneledChain opens the chain of a machine through an SSH tunnel to its REST
// API: transactions are built, simulated and broadcast against it, and signed by
// the operator's RootWallet.
type tunneledChain struct{ signer onchain.Signer }

// Open reaches the node's chain REST API from this machine.
func (c tunneledChain) Open(ctx context.Context, m Machine, chainID string) (ChainSession, error) {
	local, stop, err := m.OpenChain(ctx)
	if err != nil {
		return nil, fmt.Errorf("open an SSH tunnel to the chain on %s: %w", m.Host(), err)
	}
	client, err := onchain.New(onchain.REST{Base: local}, c.signer, chainID)
	if err != nil {
		stop()
		return nil, err
	}
	return &restSession{Client: client, base: local, http: chainHTTPClient(), stop: stop}, nil
}

// chainHTTPClient is the client the chain's REST API is read and written with: a
// node that answers with a redirect is not followed. The node is a machine setup
// has just installed, and it must not be able to send the operator's laptop to
// another URL.
func chainHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       chainReadTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// restSession reads the chain through its REST API and sends the operator's
// transactions with the embedded onchain.Client.
type restSession struct {
	*onchain.Client
	base string
	http *http.Client
	stop func()
}

func (s *restSession) Close() { s.stop() }

// get reads path. found is false for an answer that says there is no such thing;
// any other failure is an error.
func (s *restSession) get(ctx context.Context, path string, into any) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.base, "/")+path, nil)
	if err != nil {
		return false, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, chainReadLimit))
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if isNotFound(resp.StatusCode, body) {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("read %s: HTTP %d: %s", path, resp.StatusCode, tail(string(body), chainBodyShown))
	}
	if err := json.Unmarshal(body, into); err != nil {
		return false, fmt.Errorf("read %s: the answer is not the JSON expected: %w", path, err)
	}
	return true, nil
}

// isNotFound reads the gRPC gateway's way of saying "no such record": HTTP 404, or
// a 5xx whose message says not found (a module that returns a plain error for it).
func isNotFound(status int, body []byte) bool {
	return status == http.StatusNotFound || (status >= http.StatusInternalServerError && strings.Contains(strings.ToLower(string(body)), "not found"))
}

// Params reads x/nodes' parameters.
func (s *restSession) Params(ctx context.Context) (ChainParams, error) {
	var doc struct {
		Params struct {
			MinBond []struct {
				Role   string `json:"role"`
				Amount string `json:"amount"`
			} `json:"min_bond"`
			BondPerGiB string `json:"bond_per_gib"`
		} `json:"params"`
	}
	if found, err := s.get(ctx, "/orama/nodes/v1/params", &doc); err != nil || !found {
		return ChainParams{}, errors.Join(err, notThere(found, "x/nodes parameters"))
	}
	p := ChainParams{MinBond: map[int]*big.Int{}}
	for _, b := range doc.Params.MinBond {
		role, ok := roleNumber(b.Role)
		amount, okAmount := parseAmount(b.Amount)
		if !ok || !okAmount {
			return ChainParams{}, fmt.Errorf("the chain lists min_bond %q = %q, which is not a role and an amount", b.Role, b.Amount)
		}
		p.MinBond[role] = amount
	}
	var ok bool
	if p.BondPerGiB, ok = parseAmount(doc.Params.BondPerGiB); !ok {
		return ChainParams{}, fmt.Errorf("the chain's bond_per_gib %q is not a number", doc.Params.BondPerGiB)
	}
	return p, nil
}

func notThere(found bool, what string) error {
	if found {
		return nil
	}
	return fmt.Errorf("the chain has no %s", what)
}

// maxAmountDigits bounds a number the chain node reports: no amount of norama has
// more digits than this, and parsing a longer string would cost CPU for nothing.
const maxAmountDigits = 40

// parseAmount reads a non-negative integer the chain node reported.
func parseAmount(s string) (*big.Int, bool) {
	if s == "" || len(s) > maxAmountDigits || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return nil, false
	}
	return new(big.Int).SetString(s, 10)
}

// roleNumber maps the gateway's role name (ROLE_STORAGE) to clusterreg's number.
func roleNumber(name string) (int, bool) {
	roles := map[string]int{
		"VALIDATOR": clusterreg.RoleValidator, "STORAGE": clusterreg.RoleStorage, "RELAY": clusterreg.RoleRelay,
		"EXIT": clusterreg.RoleExit, "DIRAUTH": clusterreg.RoleDirauth, "ARCHIVER": clusterreg.RoleArchiver,
	}
	n, ok := roles[strings.TrimPrefix(name, roleNamePrefix)]
	return n, ok
}

// Balance is the account's spendable norama. The bank module answers zero for an
// account it has not seen.
func (s *restSession) Balance(ctx context.Context, address string) (*big.Int, error) {
	var doc struct {
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
	}
	path := "/cosmos/bank/v1beta1/balances/" + url.PathEscape(address) + "/by_denom?denom=" + denom
	found, err := s.get(ctx, path, &doc)
	if err != nil {
		return nil, err
	}
	if !found || doc.Balance.Amount == "" {
		return new(big.Int), nil
	}
	amount, ok := parseAmount(doc.Balance.Amount)
	if !ok {
		return nil, fmt.Errorf("the balance %q of %s is not an amount of norama", doc.Balance.Amount, address)
	}
	return amount, nil
}

// OperatorRegistered asks x/nodes whether address is an operator.
func (s *restSession) OperatorRegistered(ctx context.Context, address string) (bool, error) {
	var doc struct{}
	return s.get(ctx, "/orama/nodes/v1/operator/"+url.PathEscape(address), &doc)
}

// Node reads a node's roles, bonds and declared capacity; nil when unregistered.
func (s *restSession) Node(ctx context.Context, id string) (*RegisteredNode, error) {
	var doc struct {
		Node struct {
			Roles []string `json:"roles"`
			Bonds []struct {
				Role   string `json:"role"`
				Amount string `json:"amount"`
			} `json:"bonds"`
			Capacity string `json:"declared_capacity_bytes"`
			Bindings []struct {
				Service   string `json:"service"`
				KeyType   string `json:"key_type"`
				Pubkey    string `json:"pubkey"`
				Signature string `json:"signature"`
			} `json:"bindings"`
		} `json:"node"`
	}
	found, err := s.get(ctx, "/orama/nodes/v1/node/"+url.PathEscape(id), &doc)
	if err != nil || !found {
		return nil, err
	}
	n := &RegisteredNode{Bonds: map[int]*big.Int{}}
	for _, r := range doc.Node.Roles {
		if role, ok := roleNumber(r); ok {
			n.Roles = append(n.Roles, role)
		}
	}
	for _, b := range doc.Node.Bonds {
		role, ok := roleNumber(b.Role)
		amount, okAmount := parseAmount(b.Amount)
		if !ok || !okAmount {
			return nil, fmt.Errorf("node %q has a bond %q = %q that is not a role and an amount", id, b.Role, b.Amount)
		}
		n.Bonds[role] = amount
	}
	for _, b := range doc.Node.Bindings {
		binding, err := restBinding(b.Service, b.KeyType, b.Pubkey, b.Signature)
		if err != nil {
			return nil, fmt.Errorf("node %q has a binding that is not a service key: %w", id, err)
		}
		n.Bindings = append(n.Bindings, binding)
	}
	if doc.Node.Capacity != "" {
		c, ok := parseAmount(doc.Node.Capacity)
		if !ok || !c.IsUint64() {
			return nil, fmt.Errorf("node %q declares capacity %q, which is not a byte count", id, doc.Node.Capacity)
		}
		n.CapacityBytes = c.Uint64()
	}
	return n, nil
}

// restBinding reads a binding as the REST gateway names it: the key type as its
// enum name, the key and the signature in base64.
func restBinding(service, keyType, pubkey, signature string) (clusterreg.NodeBinding, error) {
	types := map[string]string{"KEY_TYPE_SECP256K1": "secp256k1", "KEY_TYPE_ED25519": "ed25519"}
	kind, ok := types[keyType]
	if !ok {
		return clusterreg.NodeBinding{}, fmt.Errorf("%q has the key type %q", service, keyType)
	}
	pub, err := base64.StdEncoding.DecodeString(pubkey)
	if err != nil {
		return clusterreg.NodeBinding{}, fmt.Errorf("%q has a pubkey that is not base64: %w", service, err)
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return clusterreg.NodeBinding{}, fmt.Errorf("%q has a signature that is not base64: %w", service, err)
	}
	return clusterreg.NodeBinding{Service: service, KeyType: kind, Pubkey: pub, Signature: sig}, nil
}

// ValidatorExists asks x/staking whether the operator has a validator.
func (s *restSession) ValidatorExists(ctx context.Context, operator string) (bool, error) {
	valoper, err := clusterreg.ValidatorAddress(operator)
	if err != nil {
		return false, err
	}
	var doc struct{}
	return s.get(ctx, "/cosmos/staking/v1beta1/validators/"+url.PathEscape(valoper), &doc)
}
