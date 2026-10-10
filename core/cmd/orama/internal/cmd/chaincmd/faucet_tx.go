package chaincmd

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/chainfaucet"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/netclass"
)

const (
	// faucetGas is the gas limit of the faucet transaction. The fee is this times the base fee.
	faucetGas = 300000
	// faucetOperatorKey is the operator key in oramad's test keyring on a stagenet or devnet node
	// (chain/scripts/stagenet/deploy.sh, create_operator_key). It signs on the node and never leaves it.
	faucetOperatorKey = "validator"
	// norama per ORAMA (chain/app/params NoramaPerOrama, 9 decimals).
	noramaPerOrama = 1_000_000_000
	// faucetMaxDigits bounds the amount before the chain's own maximum drip is asked.
	faucetMaxDigits = chainfaucet.MaxAmountDigits
)

// faucetDefaultAmount is the drip the faucet gives when no amount is asked for: 100 ORAMA, the same
// for the public faucet route.
var faucetDefaultAmount = strconv.FormatInt(chainfaucet.DefaultDripNorama, 10)

var plainDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// parseFaucetAmount reads an amount of norama: a positive plain decimal integer of at most
// faucetMaxDigits digits.
func parseFaucetAmount(s string) (*big.Int, error) {
	if !plainDecimal.MatchString(s) {
		return nil, clierr.Usage("--amount %q is not a whole number of norama (for example %s, which is 100 ORAMA)", s, faucetDefaultAmount)
	}
	if len(s) > faucetMaxDigits {
		return nil, clierr.Usage("--amount %s is too large: at most %d digits of norama", s, faucetMaxDigits)
	}
	n, _ := new(big.Int).SetString(s, 10)
	if n.Sign() == 0 {
		return nil, clierr.Usage("--amount must be more than zero")
	}
	return n, nil
}

// requireRecipient refuses anything that is not a canonical orama bech32 account, checksum
// included, before a transaction is built for it.
func requireRecipient(arg string) error {
	if _, err := clusterreg.CanonicalAccount(arg); err != nil {
		return clierr.Usage("%q is not an orama address (orama1...): %v", arg, err)
	}
	return nil
}

// requireTestNetwork refuses a chain id that is not a stagenet, devnet or localnet.
func requireTestNetwork(chainID string) error {
	if chainfaucet.IsTestNetwork(chainID) {
		return nil
	}
	return clierr.Usage("chain %q is not a test network: the faucet exists only on a chain whose id contains one of %s",
		chainID, strings.Join(netclass.NonProductionMarkers, ", "))
}

// unsignedFaucetTx is the proto-JSON of a cosmos.tx.v1beta1.Tx carrying one MsgFaucet and no
// signature. The fee amount is empty: the node fills in gas x its current base fee right before it
// signs (faucetTxScript), because a fee above the base fee is a tip and a tip needs a bank balance.
func unsignedFaucetTx(signer, recipient string, amount *big.Int) ([]byte, error) {
	tx := map[string]any{
		"body": map[string]any{
			"messages": []any{map[string]any{
				"@type":     clusterreg.FaucetTypeURL,
				"signer":    signer,
				"recipient": recipient,
				"amount":    amount.String(),
			}},
			"memo":                           "",
			"timeout_height":                 "0",
			"extension_options":              []any{},
			"non_critical_extension_options": []any{},
		},
		"auth_info": map[string]any{
			"signer_infos": []any{},
			"fee": map[string]any{
				"amount":    []any{},
				"gas_limit": fmt.Sprint(faucetGas),
				"payer":     "",
				"granter":   "",
			},
		},
		"signatures": []any{},
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the faucet transaction: %w", err)
	}
	return raw, nil
}

// orama renders norama as ORAMA with trailing zeros dropped, for the report.
func orama(norama *big.Int) string {
	whole, frac := new(big.Int).DivMod(norama, big.NewInt(noramaPerOrama), new(big.Int))
	if frac.Sign() == 0 {
		return whole.String()
	}
	f := strings.TrimRight(fmt.Sprintf("%09d", frac), "0")
	return whole.String() + "." + f
}
