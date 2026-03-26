package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

type PriceResponse struct {
	SolUSD float64 `json:"sol_usd"`
	EthUSD float64 `json:"eth_usd"`
}

var (
	priceCache     PriceResponse
	priceCacheTime time.Time
	priceMu        sync.RWMutex
	priceTTL       = 60 * time.Second
)

func PriceHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prices, err := getCachedPrices()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to fetch prices: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, prices)
	}
}

func getCachedPrices() (PriceResponse, error) {
	priceMu.RLock()
	if time.Since(priceCacheTime) < priceTTL && priceCache.SolUSD > 0 {
		cached := priceCache
		priceMu.RUnlock()
		return cached, nil
	}
	priceMu.RUnlock()

	prices, err := fetchCoinGeckoPrices()
	if err != nil {
		return PriceResponse{}, err
	}

	priceMu.Lock()
	priceCache = prices
	priceCacheTime = time.Now()
	priceMu.Unlock()

	return prices, nil
}

func fetchCoinGeckoPrices() (PriceResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.coingecko.com/api/v3/simple/price?ids=solana,ethereum&vs_currencies=usd")
	if err != nil {
		return PriceResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return PriceResponse{}, err
	}

	var data struct {
		Solana   struct{ USD float64 `json:"usd"` } `json:"solana"`
		Ethereum struct{ USD float64 `json:"usd"` } `json:"ethereum"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return PriceResponse{}, err
	}

	return PriceResponse{
		SolUSD: data.Solana.USD,
		EthUSD: data.Ethereum.USD,
	}, nil
}

// GetSOLPrice returns the cached SOL price (used by purchase verification).
func GetSOLPrice() float64 {
	priceMu.RLock()
	defer priceMu.RUnlock()
	return priceCache.SolUSD
}

// GetETHPrice returns the cached ETH price.
func GetETHPrice() float64 {
	priceMu.RLock()
	defer priceMu.RUnlock()
	return priceCache.EthUSD
}
