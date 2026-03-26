package helius

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	rpcURL   string
	apiKey   string
	http     *http.Client
	nftCache *Cache[NFTResult]
	tokCache *Cache[float64]
}

type NFTResult struct {
	HasTeamNFT      bool
	HasCommunityNFT bool
	Count           int
}

func NewClient(apiKey, rpcURL string) *Client {
	return &Client{
		rpcURL:   rpcURL,
		apiKey:   apiKey,
		http:     &http.Client{Timeout: 15 * time.Second},
		nftCache: NewCache[NFTResult](5 * time.Minute),
		tokCache: NewCache[float64](5 * time.Minute),
	}
}

// CheckNFTs checks if a wallet holds DeBros Team (100) or Community (700) NFTs.
func (c *Client) CheckNFTs(wallet, teamCollection, communityCollection string) (NFTResult, error) {
	cacheKey := "nft:" + wallet
	if cached, ok := c.nftCache.Get(cacheKey); ok {
		return cached, nil
	}

	result := NFTResult{}

	// Check team NFTs
	teamCount, err := c.searchAssetsByCollection(wallet, teamCollection)
	if err != nil {
		return result, fmt.Errorf("failed to check team NFTs: %w", err)
	}

	// Check community NFTs
	commCount, err := c.searchAssetsByCollection(wallet, communityCollection)
	if err != nil {
		return result, fmt.Errorf("failed to check community NFTs: %w", err)
	}

	result.HasTeamNFT = teamCount > 0
	result.HasCommunityNFT = commCount > 0
	result.Count = teamCount + commCount

	c.nftCache.Set(cacheKey, result)
	return result, nil
}

// GetTokenBalance returns the token balance for a specific mint.
func (c *Client) GetTokenBalance(wallet, mint string) (float64, error) {
	cacheKey := "token:" + wallet + ":" + mint
	if cached, ok := c.tokCache.Get(cacheKey); ok {
		return cached, nil
	}

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "token-balance",
		"method":  "getTokenAccountsByOwner",
		"params": []any{
			wallet,
			map[string]string{"mint": mint},
			map[string]string{"encoding": "jsonParsed"},
		},
	}

	resp, err := c.rpcCall(body)
	if err != nil {
		return 0, err
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		return 0, nil
	}

	value, ok := result["value"].([]any)
	if !ok || len(value) == 0 {
		c.tokCache.Set(cacheKey, 0)
		return 0, nil
	}

	// Parse the first token account
	account, ok := value[0].(map[string]any)
	if !ok {
		return 0, nil
	}

	balance := extractUIAmount(account)
	c.tokCache.Set(cacheKey, balance)
	return balance, nil
}

// GetTokenBalanceFresh bypasses cache (used for claims).
func (c *Client) GetTokenBalanceFresh(wallet, mint string) (float64, error) {
	cacheKey := "token:" + wallet + ":" + mint
	c.tokCache.Delete(cacheKey)
	return c.GetTokenBalance(wallet, mint)
}

func (c *Client) searchAssetsByCollection(owner, collectionAddr string) (int, error) {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "search-assets",
		"method":  "searchAssets",
		"params": map[string]any{
			"ownerAddress": owner,
			"grouping":     []any{"collection", collectionAddr},
			"page":         1,
			"limit":        1000,
		},
	}

	resp, err := c.rpcCall(body)
	if err != nil {
		return 0, err
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		return 0, nil
	}

	total, ok := result["total"].(float64)
	if ok {
		return int(total), nil
	}

	items, ok := result["items"].([]any)
	if !ok {
		return 0, nil
	}
	return len(items), nil
}

func (c *Client) rpcCall(body map[string]any) (map[string]any, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.http.Post(c.rpcURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("helius request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("helius returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if errObj, ok := result["error"]; ok {
		return nil, fmt.Errorf("helius RPC error: %v", errObj)
	}

	return result, nil
}

func extractUIAmount(account map[string]any) float64 {
	data, _ := account["account"].(map[string]any)
	if data == nil {
		return 0
	}
	dataInner, _ := data["data"].(map[string]any)
	if dataInner == nil {
		return 0
	}
	parsed, _ := dataInner["parsed"].(map[string]any)
	if parsed == nil {
		return 0
	}
	info, _ := parsed["info"].(map[string]any)
	if info == nil {
		return 0
	}
	tokenAmount, _ := info["tokenAmount"].(map[string]any)
	if tokenAmount == nil {
		return 0
	}
	uiAmount, _ := tokenAmount["uiAmount"].(float64)
	return uiAmount
}
