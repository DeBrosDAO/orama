package gateway

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/chainread"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/onchain"
)

// newFaucet starts the test-network faucet of a gateway that was given a key file, and returns nil
// for one that was not. A key file that cannot be used is an error, not a gateway without a
// faucet: the operator asked for one, and a node that quietly serves none leaves every newcomer
// who was sent to it with a refusal they cannot explain. The faucet signs against the chain
// node co-located with this gateway (the REST API chainread proxies), and refuses any chain whose
// id is not a test network's when a drip is asked for, because the chain id is only known from the
// chain.
func newFaucet(ctx context.Context, cfg *Config, logger *logging.ColoredLogger) (*chainfaucet.Service, error) {
	if cfg.FaucetKeyFile == "" {
		return nil, nil
	}
	key, err := chainfaucet.LoadKey(cfg.FaucetKeyFile)
	if err != nil {
		return nil, fmt.Errorf("gateway.faucet_key_file: %w", err)
	}
	rest := chainread.ConfigFromEnv().RESTURL
	svc, err := chainfaucet.New(ctx, key, onchain.REST{Base: rest}, chainfaucet.RESTChainID{Base: rest}, logger.Logger)
	if err != nil {
		return nil, fmt.Errorf("start the faucet: %w", err)
	}
	logger.ComponentInfo(logging.ComponentGeneral, "Faucet enabled: POST /v1/chain/faucet signs for a test network",
		zap.String("faucet_account", svc.Address()))
	return svc, nil
}
