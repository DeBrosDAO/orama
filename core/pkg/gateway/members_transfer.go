package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// walletNamespaceCap is how many namespaces one wallet may own on this cluster
// (the max_namespaces_per_wallet setting), read from the cluster registry the
// setting lives in. A transfer is held to it as a create is: a cap that only
// bound the namespaces a wallet made itself would be one anybody could be
// pushed past by a gift.
func (g *Gateway) walletNamespaceCap(ctx context.Context) (int, error) {
	if g.registry == nil {
		return 0, fmt.Errorf("the cluster registry is not available on this gateway")
	}
	policy, err := operator.LoadCreationPolicy(ctx, g.registry)
	if err != nil {
		return 0, fmt.Errorf("read the per-wallet namespace cap: %w", err)
	}
	return policy.WalletCap, nil
}

// transferRefusedMessage is what a caller is told when the wallet it names
// cannot take the namespace. It names neither the wallet nor the limit; that
// the transfer is refused is itself one bit about the wallet.
const transferRefusedMessage = "the namespace cannot be transferred to that wallet"

// refuseTransfer answers a transfer TransferOwnership did not carry out. A
// wallet at its cap is a generic 403 TRANSFER_REFUSED: the cap and the count of
// somebody else's wallet are not the caller's to learn, and a transfer is a way
// to ask for them. The refusal still tells the caller that the wallet is at its
// cap, which is all it does tell. The service has recorded which wallet and what limit in the
// audit trail. Every other refusal is the owner's request to correct.
func (g *Gateway) refuseTransfer(w http.ResponseWriter, err error) {
	var quota *auth.ErrNamespaceQuota
	if errors.As(err, &quota) {
		forbidden(w, CodeTransferRefused, transferRefusedMessage, nil)
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// refuseUnreadableCap answers a transfer whose cap could not be read: a
// retryable 503, never a transfer made without knowing the limit.
func (g *Gateway) refuseUnreadableCap(w http.ResponseWriter, err error) {
	g.logger.ComponentError(logging.ComponentGeneral, "could not read the per-wallet namespace cap; refusing the transfer", zap.Error(err))
	var bad *operator.PolicyConfigError
	if errors.As(err, &bad) {
		writeError(w, http.StatusServiceUnavailable,
			"namespace limits are not configured; an operator has to correct the cluster setting")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "the registry did not answer; try again")
}
