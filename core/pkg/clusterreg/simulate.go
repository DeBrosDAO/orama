package clusterreg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// FetchBaseFee reads x/fees' current base fee: norama per unit of gas. A
// transaction must carry at least this times its gas limit.
func FetchBaseFee(ctx context.Context, base string) (string, error) {
	body, err := getJSON(ctx, strings.TrimRight(base, "/")+"/orama/fees/v1/base-fee")
	if err != nil {
		return "", err
	}
	var resp struct {
		BaseFee string `json:"base_fee"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("base fee response is not JSON")
	}
	if !nonNegativeInteger(resp.BaseFee) {
		return "", fmt.Errorf("base fee %q is not an integer of %s", printable(resp.BaseFee), FeeDenom)
	}
	return resp.BaseFee, nil
}

// SimulateGas runs a transaction against the chain's state without including
// it and returns the gas it used. The signature may be a placeholder: a
// simulation does not verify it. A transaction the chain would refuse is an
// error carrying the chain's reason.
func SimulateGas(ctx context.Context, base string, tx []byte) (uint64, error) {
	payload, err := json.Marshal(map[string]string{"tx_bytes": base64.StdEncoding.EncodeToString(tx)})
	if err != nil {
		return 0, err
	}
	body, err := postJSON(ctx, strings.TrimRight(base, "/")+"/cosmos/tx/v1beta1/simulate", payload)
	var status *StatusError
	if errors.As(err, &status) && status.Message != "" {
		return 0, fmt.Errorf("the chain refused the transaction in simulation: %s (%w)", status.Message, err)
	}
	if err != nil {
		return 0, err
	}
	var resp struct {
		GasInfo struct {
			GasUsed string `json:"gas_used"`
		} `json:"gas_info"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("simulation response is not JSON")
	}
	used, err := parseUint(resp.GasInfo.GasUsed)
	if err != nil || used == 0 {
		return 0, fmt.Errorf("simulation reported no gas used (%q)", printable(resp.GasInfo.GasUsed))
	}
	return used, nil
}

func nonNegativeInteger(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
