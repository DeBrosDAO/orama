package handler

import (
	"fmt"
	"net/http"

	"github.com/debros/orama-website/invest-api/auth"
	"github.com/debros/orama-website/invest-api/db"
	"github.com/debros/orama-website/invest-api/helius"
)

func NftStatusHandler(heliusClient *helius.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wallet, _, ok := auth.WalletFromContext(r.Context())
		if !ok {
			jsonError(w, http.StatusUnauthorized, "wallet required")
			return
		}

		result, err := heliusClient.CheckNFTs(wallet, db.TeamNFTCollection, db.CommunityNFTCollection)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to check NFTs: %v", err))
			return
		}

		jsonResponse(w, http.StatusOK, db.NftStatusResponse{
			HasTeamNFT:      result.HasTeamNFT,
			HasCommunityNFT: result.HasCommunityNFT,
			NFTCount:        result.Count,
		})
	}
}
