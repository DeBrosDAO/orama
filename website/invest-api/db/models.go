package db

// Constants shared across handlers.
const (
	TotalTokenAllocation = 10_000_000
	TokenPrice           = 0.05
	MinTokenPurchaseUSD  = 50.0
	TotalLicenses        = 500
	LicensePrice         = 3000.0

	TeamNFTCollection      = "4vd4ct4ohhSsjKdi2QKdRTcEPsgFHMTNwCUR27xNrA73"
	CommunityNFTCollection = "DV8pjrqEKx7ET5FSFY2pCugnzyZmcDFrWFxzDLqdSzmp"
	AnchatMint             = "EZGb7aaSbCHbE6DcfynezKRc9Bkzxs8stP97H6WEpump"
	AnchatClaimRate        = 0.0025 // 0.25%
	AnchatMinBalance       = 10_000 // Minimum $ANCHAT to be eligible for claim

	TreasurySOL = "CBRv5D69LKD8xmnjcNNcz1jgehEtKzhUdrxdFaBs14CP"
	TreasuryETH = "0xA3b5Cba1dBD45951F718d1720bE19718159f5B0D"
	TreasuryBTC = "bc1qzpkjguxh4pl9pdhj76zeztur42prhfed2hd22z"
)

type StatsResponse struct {
	TokensSold      float64 `json:"tokens_sold"`
	TokensRemaining float64 `json:"tokens_remaining"`
	TokenRaised     float64 `json:"token_raised"`
	LicensesSold    int     `json:"licenses_sold"`
	LicensesLeft    int     `json:"licenses_left"`
	LicenseRaised   float64 `json:"license_raised"`
	TotalRaised     float64 `json:"total_raised"`
	WhitelistCount  int     `json:"whitelist_count"`
}

type ActivityEntry struct {
	EventType string `json:"event_type"`
	Wallet    string `json:"wallet"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

type MeResponse struct {
	Wallet          string           `json:"wallet"`
	Chain           string           `json:"chain"`
	TokensPurchased float64          `json:"tokens_purchased"`
	TokenSpent      float64          `json:"tokens_spent"`
	Licenses        []LicenseInfo    `json:"licenses"`
	OnWhitelist     bool             `json:"on_whitelist"`
	PurchaseHistory []PurchaseRecord `json:"purchase_history"`
	AnchatClaimed   bool             `json:"anchat_claimed"`
	AnchatOrama     float64          `json:"anchat_orama_amount"`
}

type LicenseInfo struct {
	LicenseNumber int    `json:"license_number"`
	ClaimedViaNFT bool   `json:"claimed_via_nft"`
	PurchasedAt   string `json:"purchased_at"`
}

type PurchaseRecord struct {
	Type      string  `json:"type"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	TxHash    string  `json:"tx_hash"`
	CreatedAt string  `json:"created_at"`
}

type NftStatusResponse struct {
	HasTeamNFT      bool `json:"has_team_nft"`
	HasCommunityNFT bool `json:"has_community_nft"`
	NFTCount        int  `json:"nft_count"`
}

type AnchatBalanceResponse struct {
	Balance       float64 `json:"balance"`
	ClaimableOrama float64 `json:"claimable_orama"`
}
