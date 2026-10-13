package runner

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
)

const (
	// earningsWait bounds how long the run waits for the funder's seat to earn its first fee.
	earningsWait = 4 * time.Minute
	earningsPoll = 2 * time.Second
	// fundTopUpDivisor: an account holding less than 1/fundTopUpDivisor of a drip is topped up.
	fundTopUpDivisor = 2
)

// ErrNoFaucet is returned when the chain's genesis did not enable the test-network faucet.
var ErrNoFaucet = errors.New("the faucet is off in this genesis; start the localnet with FAUCET_ENABLED=1")

// LoadFunder reads the faucet signer's key from a file holding 64 hex characters, the output of
// `localnet.sh funder-key`.
func LoadFunder(path string) (tx.Account, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return tx.Account{}, fmt.Errorf("read the funder key: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return tx.Account{}, fmt.Errorf("the funder key file %s is not hex: %w", path, err)
	}
	return tx.DeriveAccount(key)
}

// fundAccounts tops every bench account up from the faucet. It waits for the funder to have earned
// the fee its drips cost: a localnet's seat earns from the second epoch on.
func fundAccounts(ctx context.Context, chain *ops.Chain, funder tx.Account, accounts []tx.Account, logf func(string, ...any)) error {
	var params emissiontypes.QueryParamsResponse
	if err := chain.Client.Query(ctx, "/orama.emission.v1.Query/Params", &emissiontypes.QueryParamsRequest{}, &params); err != nil {
		return fmt.Errorf("read the emission params: %w", err)
	}
	if !params.Params.FaucetEnabled {
		return ErrNoFaucet
	}
	drip := params.Params.FaucetMaxDrip
	need, err := accountsToFund(ctx, chain, accounts, drip)
	if err != nil {
		return err
	}
	if len(need) == 0 {
		return nil
	}
	if err := waitEarnings(ctx, chain, funder.Address, logf); err != nil {
		return err
	}
	logf("funding %d accounts with %s norama each from the faucet", len(need), drip)
	msgs := make([]sdk.Msg, len(need))
	for i, a := range need {
		msgs[i] = &emissiontypes.MsgFaucet{Signer: funder.Address, Recipient: a.Address, Amount: drip}
	}
	// The seat holds earnings, not a bank balance, so its fee is exactly the base fee: a tip above
	// it would have to come from the bank.
	price, err := chain.BasePrice(ctx)
	if err != nil {
		return err
	}
	if err := chain.BatchPriced(ctx, funder, 0, price, ops.DefaultBatchTimeout, msgs...); err != nil {
		return fmt.Errorf("drip to the bench accounts: %w", err)
	}
	return verifyFunded(ctx, chain, need, drip)
}

// accountsToFund lists the accounts holding less than half a drip.
func accountsToFund(ctx context.Context, chain *ops.Chain, accounts []tx.Account, drip math.Int) ([]tx.Account, error) {
	threshold := drip.QuoRaw(fundTopUpDivisor)
	var need []tx.Account
	for _, a := range accounts {
		bal, err := chain.Client.Balance(ctx, a.Address)
		if err != nil {
			return nil, fmt.Errorf("read the balance of %s: %w", a.Address, err)
		}
		if bal.LT(threshold) {
			need = append(need, a)
		}
	}
	return need, nil
}

func verifyFunded(ctx context.Context, chain *ops.Chain, funded []tx.Account, drip math.Int) error {
	for _, a := range funded {
		bal, err := chain.Client.Balance(ctx, a.Address)
		if err != nil {
			return err
		}
		if bal.LT(drip.QuoRaw(fundTopUpDivisor)) {
			return fmt.Errorf("account %s holds %s after its drip: the faucet transaction did not execute", a.Address, bal)
		}
	}
	return nil
}

// waitEarnings waits until the funder has earnings or a bank balance to pay fees from.
func waitEarnings(ctx context.Context, chain *ops.Chain, addr string, logf func(string, ...any)) error {
	deadline := time.NewTimer(earningsWait)
	defer deadline.Stop()
	tick := time.NewTicker(earningsPoll)
	defer tick.Stop()
	for waited := false; ; waited = true {
		funds, err := feePower(ctx, chain, addr)
		if err != nil {
			return err
		}
		if funds.IsPositive() {
			return nil
		}
		if !waited {
			logf("waiting for the faucet seat %s to earn its first fee (a localnet seat earns from the second epoch)", addr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("the faucet seat %s earned nothing in %s; is the chain producing blocks and past its first epoch?", addr, earningsWait)
		case <-tick.C:
		}
	}
}

// feePower is what addr can pay a fee from: bank balance plus earnings.
func feePower(ctx context.Context, chain *ops.Chain, addr string) (math.Int, error) {
	bank, err := chain.Client.Balance(ctx, addr)
	if err != nil {
		return math.Int{}, err
	}
	var earned feestypes.QueryEarningsResponse
	if err := chain.Client.Query(ctx, "/orama.fees.v1.Query/Earnings", &feestypes.QueryEarningsRequest{Address: addr}, &earned); err != nil {
		return math.Int{}, fmt.Errorf("read the earnings of %s: %w", addr, err)
	}
	if earned.Balance.IsNil() {
		return bank, nil
	}
	return bank.Add(earned.Balance), nil
}
