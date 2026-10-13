package clusterreg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// latestBlockPath is the Cosmos REST route of the newest block.
const latestBlockPath = "/cosmos/base/tendermint/v1beta1/blocks/latest"

// FetchLatestHeight reads the height of the newest block from the Cosmos REST API at base. A
// transaction's timeout height is counted from it.
func FetchLatestHeight(ctx context.Context, base string) (uint64, error) {
	body, err := getJSON(ctx, strings.TrimRight(base, "/")+latestBlockPath)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Block struct {
			Header struct {
				Height string `json:"height"`
			} `json:"header"`
		} `json:"block"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("latest block response is not JSON")
	}
	height, err := parseUint(resp.Block.Header.Height)
	if err != nil || height == 0 {
		return 0, fmt.Errorf("the latest block has no usable height (%q)", printable(resp.Block.Header.Height))
	}
	return height, nil
}
