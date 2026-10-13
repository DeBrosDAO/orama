package gateway

import (
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/clientkey"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// newCallerProof loads the secret this node's Caddy proves itself with. Behind Caddy every public
// request reaches the gateway from 127.0.0.1, so X-Forwarded-For is the only thing that tells the
// callers apart, and a process on the node can write it as well as Caddy can; the gateway believes
// it only on a request that carries the secret (clientkey). Without the file no request is
// proven, the public callers share the loopback address's buckets, and the gateway says so.
func newCallerProof(logger *logging.ColoredLogger) *clientkey.Prover {
	return clientkey.NewProver(constants.CaddyProofFile, func(err error) {
		logger.ComponentWarn(logging.ComponentGeneral, "Caddy cannot prove itself to this gateway", zap.Error(err))
	})
}
