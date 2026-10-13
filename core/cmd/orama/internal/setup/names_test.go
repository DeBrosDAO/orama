package setup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/onchain"
)

type fakeNameChain struct {
	held     map[string]string
	queryErr error
	claimErr error
	claims   []string
}

func (f *fakeNameChain) NodeName(_ context.Context, nodeID string) (string, error) {
	return f.held[nodeID], f.queryErr
}

func (f *fakeNameChain) ClaimNodeName(_ context.Context, nodeID, name string) (*onchain.Receipt, error) {
	f.claims = append(f.claims, nodeID+"="+name)
	return &onchain.Receipt{}, f.claimErr
}

func TestChainNames_claimsAFreeName(t *testing.T) {
	chain := &fakeNameChain{}
	if err := (ChainNames{}).Claim(context.Background(), chain, "alice", "alice"); err != nil {
		t.Fatal(err)
	}
	if len(chain.claims) != 1 || chain.claims[0] != "alice=alice" {
		t.Errorf("claims = %v", chain.claims)
	}
}

func TestChainNames_aNodeThatHoldsTheNameIsDoneAndSendsNothing(t *testing.T) {
	chain := &fakeNameChain{held: map[string]string{"alice": "alice"}}
	if err := (ChainNames{}).Claim(context.Background(), chain, "alice", "alice"); err != nil {
		t.Fatalf("holding the name is not an error: %v", err)
	}
	if len(chain.claims) != 0 {
		t.Errorf("a name the node holds was claimed again: %v", chain.claims)
	}
}

func TestChainNames_aNodeThatHoldsAnotherNameIsAnError(t *testing.T) {
	chain := &fakeNameChain{held: map[string]string{"alice": "other"}}
	err := (ChainNames{}).Claim(context.Background(), chain, "alice", "alice")
	if err == nil || !strings.Contains(err.Error(), `already holds the name "other"`) {
		t.Fatalf("err = %v", err)
	}
	if len(chain.claims) != 0 {
		t.Errorf("claimed over a name the node holds: %v", chain.claims)
	}
}

func TestChainNames_aNameTheChainWouldRefuseIsRefusedBeforeAnyQuery(t *testing.T) {
	for _, name := range []string{"", "ab", "test", "seed1", "xn--abc", "-alice", "alice-", "Alice", strings.Repeat("a", 33)} {
		chain := &fakeNameChain{queryErr: errors.New("must not be asked")}
		err := (ChainNames{}).Claim(context.Background(), chain, name, "alice")
		if err == nil || strings.Contains(err.Error(), "must not be asked") {
			t.Errorf("name %q: err = %v", name, err)
		}
		if len(chain.claims) != 0 {
			t.Errorf("name %q was claimed", name)
		}
	}
}

func TestChainNames_chainFailuresAreWrappedWithWhatWasTried(t *testing.T) {
	err := (ChainNames{}).Claim(context.Background(), &fakeNameChain{queryErr: errors.New("node down")}, "alice", "alice")
	if err == nil || !strings.Contains(err.Error(), "node down") || !strings.Contains(err.Error(), "holds a name") {
		t.Errorf("query: err = %v", err)
	}
	err = (ChainNames{}).Claim(context.Background(), &fakeNameChain{claimErr: errors.New("name already claimed")}, "alice", "alice")
	if err == nil || !strings.Contains(err.Error(), "name already claimed") || !strings.Contains(err.Error(), `"alice"`) {
		t.Errorf("claim: err = %v", err)
	}
}
