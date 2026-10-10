//go:build e2e_fleet

package authclusteradmin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// pathTransfer is the owner's hand-over of a namespace.
	pathTransfer = "/v1/namespace/members/transfer"
	// handBackBudget bounds returning a namespace to its creator.
	handBackBudget = time.Minute
)

// TestWalletCap_transferIsHeldToTheCap: with the per-wallet cap at 1, a wallet
// that already owns a namespace cannot be handed a second one (a generic 403
// TRANSFER_REFUSED that says nothing of the recipient); the owner keeps the
// namespace and can hand it to a wallet that owns nothing
// (docs/whitepaper/technical-reference/vol1/14-authorization.md: a transfer is held to the per-wallet cap).
func TestWalletCap_transferIsHeldToTheCap(t *testing.T) {
	f := harness.Fleet(t)
	ns.Hold(t, f, 2)
	giver := ns.New(t, f, ns.Options{})
	holder := ns.New(t, f, ns.Options{})
	cli := harness.CLI(t)
	setSetting(t, cli, settingCap, "1")

	full := holder.Owner.Wallet.Address()
	r := postJSON(t, giver.Owner.Client, pathTransfer, giver.Owner.Token(), map[string]string{"wallet": full})
	expectCode(t, r, http.StatusForbidden, codeTransferRefused)
	var refusal map[string]any
	if err := json.Unmarshal(r.Body, &refusal); err != nil {
		t.Fatalf("the refusal is not JSON: %v %s", err, r.Body)
	}
	for _, leak := range []string{"limit", "wallet"} {
		if _, ok := refusal[leak]; ok {
			t.Errorf("the refusal tells the caller about the recipient (%s): %s", leak, r.Body)
		}
	}
	if strings.Contains(string(r.Body), full) {
		t.Errorf("the refusal names the recipient wallet: %s", r.Body)
	}

	// Refused means nothing moved: the giver still owns the namespace, so it is
	// still the one that can hand it on.
	empty := newWallet(t)
	postJSON(t, giver.Owner.Client, pathTransfer, giver.Owner.Token(), map[string]string{"wallet": empty.Address()}).
		Expect(t, http.StatusOK)
	t.Cleanup(func() { handBack(t, giver, empty) })
}

// handBack returns the namespace to its creator so the harness can delete it as
// the owner it signed in as.
func handBack(t testing.TB, n *ns.Namespace, from *wallet.EVM) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), handBackBudget)
	defer cancel()
	s, err := n.Owner.Client.SignIn(ctx, from, n.Name, nil)
	if err != nil {
		t.Errorf("cleanup: sign in as the new owner of %s: %v", n.Name, err)
		return
	}
	if _, err := n.Owner.Client.JSON(ctx, http.MethodPost, pathTransfer, s.AccessToken,
		map[string]string{"wallet": n.Owner.Wallet.Address()}, nil); err != nil {
		t.Errorf("cleanup: hand %s back to its creator: %v", n.Name, err)
	}
}
