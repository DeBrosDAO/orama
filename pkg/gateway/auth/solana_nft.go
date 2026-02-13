package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const (
	// Solana Token Program ID
	tokenProgramID = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	// Metaplex Token Metadata Program ID
	metaplexProgramID = "metaqbxxUerdq28cj1RbAWkYQm3ybzjb6a8bt518x1s"
)

// SolanaNFTVerifier verifies NFT ownership on Solana via JSON-RPC.
type SolanaNFTVerifier struct {
	rpcURL            string
	collectionAddress string
	httpClient        *http.Client
}

// NewSolanaNFTVerifier creates a new verifier for the given collection.
func NewSolanaNFTVerifier(rpcURL, collectionAddress string) *SolanaNFTVerifier {
	return &SolanaNFTVerifier{
		rpcURL:            rpcURL,
		collectionAddress: collectionAddress,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// VerifyNFTOwnership checks if the wallet owns at least one NFT from the configured collection.
func (v *SolanaNFTVerifier) VerifyNFTOwnership(ctx context.Context, walletAddress string) (bool, error) {
	// 1. Get all token accounts owned by the wallet
	tokenAccounts, err := v.getTokenAccountsByOwner(ctx, walletAddress)
	if err != nil {
		return false, fmt.Errorf("failed to get token accounts: %w", err)
	}

	// 2. Filter for NFT-like accounts (amount == 1, decimals == 0)
	var mints []string
	for _, ta := range tokenAccounts {
		if ta.amount == "1" && ta.decimals == 0 {
			mints = append(mints, ta.mint)
		}
	}

	if len(mints) == 0 {
		return false, nil
	}

	// Cap mints to prevent excessive RPC calls from wallets with many tokens
	const maxMints = 500
	if len(mints) > maxMints {
		mints = mints[:maxMints]
	}

	// 3. Derive metadata PDA for each mint
	metaplexProgram, err := base58Decode(metaplexProgramID)
	if err != nil {
		return false, fmt.Errorf("failed to decode metaplex program: %w", err)
	}

	var pdas []string
	for _, mint := range mints {
		mintBytes, err := base58Decode(mint)
		if err != nil || len(mintBytes) != 32 {
			continue
		}
		pda, err := findProgramAddress(
			[][]byte{[]byte("metadata"), metaplexProgram, mintBytes},
			metaplexProgram,
		)
		if err != nil {
			continue
		}
		pdas = append(pdas, base58Encode(pda))
	}

	if len(pdas) == 0 {
		return false, nil
	}

	// 4. Batch fetch metadata accounts (max 100 per call)
	targetCollection, err := base58Decode(v.collectionAddress)
	if err != nil {
		return false, fmt.Errorf("failed to decode collection address: %w", err)
	}

	for i := 0; i < len(pdas); i += 100 {
		end := i + 100
		if end > len(pdas) {
			end = len(pdas)
		}
		batch := pdas[i:end]

		accounts, err := v.getMultipleAccounts(ctx, batch)
		if err != nil {
			return false, fmt.Errorf("failed to get metadata accounts: %w", err)
		}

		for _, acct := range accounts {
			if acct == nil {
				continue
			}
			collKey, verified := parseMetaplexCollection(acct)
			if verified && bytes.Equal(collKey, targetCollection) {
				return true, nil
			}
		}
	}

	return false, nil
}

// tokenAccountInfo holds parsed SPL token account data.
type tokenAccountInfo struct {
	mint     string
	amount   string
	decimals int
}

// getTokenAccountsByOwner fetches all SPL token accounts for a wallet.
func (v *SolanaNFTVerifier) getTokenAccountsByOwner(ctx context.Context, wallet string) ([]tokenAccountInfo, error) {
	params := []interface{}{
		wallet,
		map[string]string{"programId": tokenProgramID},
		map[string]string{"encoding": "jsonParsed"},
	}

	result, err := v.rpcCall(ctx, "getTokenAccountsByOwner", params)
	if err != nil {
		return nil, err
	}

	// Parse the result
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result format")
	}

	valueArr, ok := resultMap["value"].([]interface{})
	if !ok {
		return nil, nil
	}

	var accounts []tokenAccountInfo
	for _, item := range valueArr {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		account, ok := itemMap["account"].(map[string]interface{})
		if !ok {
			continue
		}
		data, ok := account["data"].(map[string]interface{})
		if !ok {
			continue
		}
		parsed, ok := data["parsed"].(map[string]interface{})
		if !ok {
			continue
		}
		info, ok := parsed["info"].(map[string]interface{})
		if !ok {
			continue
		}

		mint, _ := info["mint"].(string)
		tokenAmount, ok := info["tokenAmount"].(map[string]interface{})
		if !ok {
			continue
		}
		amount, _ := tokenAmount["amount"].(string)
		decimals, _ := tokenAmount["decimals"].(float64)

		if mint != "" && amount != "" {
			accounts = append(accounts, tokenAccountInfo{
				mint:     mint,
				amount:   amount,
				decimals: int(decimals),
			})
		}
	}

	return accounts, nil
}

// getMultipleAccounts fetches multiple accounts by their addresses.
// Returns raw account data (base64-decoded) for each address, nil for missing accounts.
func (v *SolanaNFTVerifier) getMultipleAccounts(ctx context.Context, addresses []string) ([][]byte, error) {
	params := []interface{}{
		addresses,
		map[string]string{"encoding": "base64"},
	}

	result, err := v.rpcCall(ctx, "getMultipleAccounts", params)
	if err != nil {
		return nil, err
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result format")
	}

	valueArr, ok := resultMap["value"].([]interface{})
	if !ok {
		return nil, nil
	}

	accounts := make([][]byte, len(valueArr))
	for i, item := range valueArr {
		if item == nil {
			continue
		}
		acct, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		dataArr, ok := acct["data"].([]interface{})
		if !ok || len(dataArr) < 1 {
			continue
		}
		dataStr, ok := dataArr[0].(string)
		if !ok {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			continue
		}
		accounts[i] = decoded
	}

	return accounts, nil
}

// rpcCall executes a Solana JSON-RPC call.
func (v *SolanaNFTVerifier) rpcCall(ctx context.Context, method string, params []interface{}) (interface{}, error) {
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", v.rpcURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create RPC request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RPC request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RPC returned HTTP %d", resp.StatusCode)
	}

	// Limit response size to 10MB to prevent memory exhaustion
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read RPC response: %w", err)
	}

	var rpcResp struct {
		Result interface{}            `json:"result"`
		Error  map[string]interface{} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to parse RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		msg, _ := rpcResp.Error["message"].(string)
		return nil, fmt.Errorf("RPC error: %s", msg)
	}

	return rpcResp.Result, nil
}

// parseMetaplexCollection extracts the collection key and verified flag from
// Borsh-encoded Metaplex metadata account data.
//
// Metaplex Token Metadata v1 layout (simplified):
//   - [0]:     key (1 byte, should be 4 for MetadataV1)
//   - [1..33]: update_authority (32 bytes)
//   - [33..65]: mint (32 bytes)
//   - [65..]:  name (4-byte len prefix + UTF-8, borsh string)
//   - then:    symbol (borsh string)
//   - then:    uri (borsh string)
//   - then:    seller_fee_basis_points (u16, 2 bytes)
//   - then:    creators (Option<Vec<Creator>>)
//   - then:    primary_sale_happened (bool, 1 byte)
//   - then:    is_mutable (bool, 1 byte)
//   - then:    edition_nonce (Option<u8>)
//   - then:    token_standard (Option<u8>)
//   - then:    collection (Option<Collection>)
//   - Collection: { verified: bool(1), key: Pubkey(32) }
func parseMetaplexCollection(data []byte) (collectionKey []byte, verified bool) {
	if len(data) < 66 {
		return nil, false
	}

	// Validate metadata key byte (must be 4 = MetadataV1)
	if data[0] != 4 {
		return nil, false
	}

	// Skip: key(1) + update_authority(32) + mint(32)
	offset := 65

	// Skip name (borsh string: 4-byte LE length + bytes)
	offset, _ = skipBorshString(data, offset)
	if offset < 0 {
		return nil, false
	}

	// Skip symbol
	offset, _ = skipBorshString(data, offset)
	if offset < 0 {
		return nil, false
	}

	// Skip uri
	offset, _ = skipBorshString(data, offset)
	if offset < 0 {
		return nil, false
	}

	// Skip seller_fee_basis_points (u16)
	offset += 2
	if offset > len(data) {
		return nil, false
	}

	// Skip creators (Option<Vec<Creator>>)
	// Option: 1 byte (0 = None, 1 = Some)
	if offset >= len(data) {
		return nil, false
	}
	if data[offset] == 1 {
		offset++ // skip option byte
		if offset+4 > len(data) {
			return nil, false
		}
		numCreators := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		// Solana limits creators to 5, but be generous with 20
		if numCreators > 20 {
			return nil, false
		}
		// Each Creator: pubkey(32) + verified(1) + share(1) = 34 bytes
		creatorBytes := numCreators * 34
		if offset+creatorBytes > len(data) {
			return nil, false
		}
		offset += creatorBytes
	} else {
		offset++ // skip None byte
	}

	if offset >= len(data) {
		return nil, false
	}

	// Skip primary_sale_happened (bool)
	offset++
	if offset >= len(data) {
		return nil, false
	}

	// Skip is_mutable (bool)
	offset++
	if offset >= len(data) {
		return nil, false
	}

	// Skip edition_nonce (Option<u8>)
	if offset >= len(data) {
		return nil, false
	}
	if data[offset] == 1 {
		offset += 2 // option byte + u8
	} else {
		offset++ // None
	}

	// Skip token_standard (Option<u8>)
	if offset >= len(data) {
		return nil, false
	}
	if data[offset] == 1 {
		offset += 2
	} else {
		offset++
	}

	// Collection (Option<Collection>)
	if offset >= len(data) {
		return nil, false
	}
	if data[offset] == 0 {
		// No collection
		return nil, false
	}
	offset++ // skip option byte

	// Collection: verified(1 byte bool) + key(32 bytes)
	if offset+33 > len(data) {
		return nil, false
	}
	verified = data[offset] == 1
	offset++
	collectionKey = data[offset : offset+32]

	return collectionKey, verified
}

// skipBorshString skips a Borsh-encoded string (4-byte LE length + bytes) at the given offset.
// Returns the new offset, or -1 if the data is too short.
func skipBorshString(data []byte, offset int) (int, string) {
	if offset+4 > len(data) {
		return -1, ""
	}
	strLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if offset+strLen > len(data) {
		return -1, ""
	}
	s := string(data[offset : offset+strLen])
	return offset + strLen, s
}

// findProgramAddress derives a Solana Program Derived Address (PDA).
// It finds the first valid PDA by trying bump seeds from 255 down to 0.
func findProgramAddress(seeds [][]byte, programID []byte) ([]byte, error) {
	for bump := byte(255); ; bump-- {
		candidate := derivePDA(seeds, bump, programID)
		if !isOnCurve(candidate) {
			return candidate, nil
		}
		if bump == 0 {
			break
		}
	}
	return nil, fmt.Errorf("could not find valid PDA")
}

// derivePDA computes SHA256(seeds || bump || programID || "ProgramDerivedAddress").
func derivePDA(seeds [][]byte, bump byte, programID []byte) []byte {
	h := sha256.New()
	for _, seed := range seeds {
		h.Write(seed)
	}
	h.Write([]byte{bump})
	h.Write(programID)
	h.Write([]byte("ProgramDerivedAddress"))
	return h.Sum(nil)
}

// isOnCurve checks if a 32-byte key is on the Ed25519 curve.
// PDAs must NOT be on the curve (they have no private key).
// This uses a simplified check based on the Ed25519 point decompression.
func isOnCurve(key []byte) bool {
	if len(key) != 32 {
		return false
	}

	// Ed25519 field prime: p = 2^255 - 19
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))

	// Extract y coordinate (little-endian, clear top bit)
	yBytes := make([]byte, 32)
	copy(yBytes, key)
	yBytes[31] &= 0x7f

	// Reverse for big-endian
	for i, j := 0, len(yBytes)-1; i < j; i, j = i+1, j-1 {
		yBytes[i], yBytes[j] = yBytes[j], yBytes[i]
	}

	y := new(big.Int).SetBytes(yBytes)
	if y.Cmp(p) >= 0 {
		return false
	}

	// Compute u = y^2 - 1
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, p)
	u := new(big.Int).Sub(y2, big.NewInt(1))
	u.Mod(u, p)
	if u.Sign() < 0 {
		u.Add(u, p)
	}

	// d = -121665/121666 mod p
	d := new(big.Int).SetInt64(121666)
	d.ModInverse(d, p)
	d.Mul(d, big.NewInt(-121665))
	d.Mod(d, p)
	if d.Sign() < 0 {
		d.Add(d, p)
	}

	// Compute v = d*y^2 + 1
	v := new(big.Int).Mul(d, y2)
	v.Mod(v, p)
	v.Add(v, big.NewInt(1))
	v.Mod(v, p)

	// Check if u/v is a quadratic residue mod p
	// x^2 = u * v^{-1} mod p
	vInv := new(big.Int).ModInverse(v, p)
	if vInv == nil {
		return false
	}
	x2 := new(big.Int).Mul(u, vInv)
	x2.Mod(x2, p)

	// Euler criterion: x2^((p-1)/2) mod p == 1 means QR
	exp := new(big.Int).Sub(p, big.NewInt(1))
	exp.Rsh(exp, 1)
	result := new(big.Int).Exp(x2, exp, p)

	return result.Cmp(big.NewInt(1)) == 0 || x2.Sign() == 0
}

// base58Decode decodes a base58-encoded string (same as Service.Base58Decode but standalone).
func base58Decode(input string) ([]byte, error) {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	answer := big.NewInt(0)
	j := big.NewInt(1)
	for i := len(input) - 1; i >= 0; i-- {
		tmp := strings.IndexByte(alphabet, input[i])
		if tmp == -1 {
			return nil, fmt.Errorf("invalid base58 character")
		}
		idx := big.NewInt(int64(tmp))
		tmp1 := new(big.Int).Mul(idx, j)
		answer.Add(answer, tmp1)
		j.Mul(j, big.NewInt(58))
	}
	res := answer.Bytes()
	for i := 0; i < len(input) && input[i] == alphabet[0]; i++ {
		res = append([]byte{0}, res...)
	}
	return res, nil
}

// base58Encode encodes bytes to base58.
func base58Encode(input []byte) string {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)

	var result []byte
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		result = append(result, alphabet[mod.Int64()])
	}

	// Leading zeros
	for _, b := range input {
		if b != 0 {
			break
		}
		result = append(result, alphabet[0])
	}

	// Reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}
