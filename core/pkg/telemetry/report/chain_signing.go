package report

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	signingPageLimit = 200
	signingMaxPages  = 5
	signingBudget    = 8 * time.Second
)

// SigningView is this validator's slashing and staking answers.
// Error is set when a query failed or a validator with voting power was
// missing from the set. A node that is not in the set and has no voting
// power leaves the pointers nil and Error empty.
type SigningView struct {
	MissedBlockRatio   *float64
	MinSignedPerWindow *float64
	Jailed             *bool
	Tombstoned         *bool
	Error              string
}

// readChainSigning fills the slashing and staking fields. It does not change
// Responsive: a REST failure is SigningError, and the CometBFT section stands.
func readChainSigning(r *ChainReport, apiBase string) {
	if r == nil || r.ConsAddress == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), signingBudget)
	defer cancel()
	view := fetchSigning(ctx, apiBase, r.ConsAddress, r.VotingPower)
	r.MissedBlockRatio = view.MissedBlockRatio
	r.MinSignedPerWindow = view.MinSignedPerWindow
	r.Jailed = view.Jailed
	r.Tombstoned = view.Tombstoned
	r.SigningError = view.Error
}

func fetchSigning(ctx context.Context, apiBase, consHex string, votingPower int64) SigningView {
	var view SigningView
	if !chainValidatorAddressRe.MatchString(consHex) {
		view.Error = "consensus address is not 40 upper-case hex characters"
		return view
	}
	paramsBody, err := httpGet(ctx, apiBase+"/cosmos/slashing/v1beta1/params")
	if err != nil {
		view.Error = shortChainError(fmt.Errorf("slashing params: %w", err))
		return view
	}
	window, minSigned, err := parseSlashingParams(paramsBody)
	if err != nil {
		view.Error = shortChainError(err)
		return view
	}
	signingPages, err := getPages(ctx, apiBase+"/cosmos/slashing/v1beta1/signing_infos")
	if err != nil {
		view.Error = shortChainError(fmt.Errorf("signing infos: %w", err))
		return view
	}
	stakingPages, err := getPages(ctx, apiBase+"/cosmos/staking/v1beta1/validators")
	if err != nil {
		view.Error = shortChainError(fmt.Errorf("staking validators: %w", err))
		return view
	}
	return interpretSigning(consHex, votingPower, window, minSigned, signingPages, stakingPages)
}

func getPages(ctx context.Context, path string) ([][]byte, error) {
	var pages [][]byte
	next := ""
	for i := 0; i < signingMaxPages; i++ {
		u := path + "?pagination.limit=" + strconv.Itoa(signingPageLimit)
		if next != "" {
			u += "&pagination.key=" + url.QueryEscape(next)
		}
		body, err := httpGet(ctx, u)
		if err != nil {
			return nil, err
		}
		pages = append(pages, body)
		key, err := pageNextKey(body)
		if err != nil {
			return nil, err
		}
		if key == "" {
			return pages, nil
		}
		next = key
	}
	return nil, fmt.Errorf("more than %d pages", signingMaxPages*signingPageLimit)
}

// InterpretSigning is the pure part of the slashing and staking read.
// window and minSigned come from the slashing params. Pages are the raw
// JSON bodies, in order. votingPower is this node's power from /status;
// zero means a missing row is not an error unless the row is found jailed.
func InterpretSigning(consHex string, votingPower int64, paramsBody []byte, signingPages, stakingPages [][]byte) SigningView {
	window, minSigned, err := parseSlashingParams(paramsBody)
	if err != nil {
		return SigningView{Error: shortChainError(err)}
	}
	return interpretSigning(consHex, votingPower, window, minSigned, signingPages, stakingPages)
}

func interpretSigning(consHex string, votingPower int64, window int64, minSigned float64, signingPages, stakingPages [][]byte) SigningView {
	var view SigningView
	if window <= 0 || minSigned < 0 || minSigned > 1 {
		view.Error = "slashing params are outside their range"
		return view
	}
	raw, err := hex.DecodeString(consHex)
	if err != nil || len(raw) != 20 {
		view.Error = "consensus address is not 20 bytes"
		return view
	}
	foundSign, missed, tomb, err := findSigning(raw, signingPages)
	if err != nil {
		view.Error = shortChainError(err)
		return view
	}
	foundStake, jailed, err := findJailed(raw, stakingPages)
	if err != nil {
		view.Error = shortChainError(err)
		return view
	}
	isValidator := votingPower > 0 || foundStake
	if !foundSign && !foundStake && !isValidator {
		return view
	}
	if !foundSign && isValidator {
		view.Error = "validator is not in the slashing signing infos"
	}
	if !foundStake && isValidator {
		if view.Error != "" {
			view.Error += "; "
		}
		view.Error += "validator is not in the staking list"
	}
	if foundSign {
		ratio := float64(missed) / float64(window)
		view.MissedBlockRatio = &ratio
		view.MinSignedPerWindow = &minSigned
		view.Tombstoned = &tomb
	}
	if foundStake {
		view.Jailed = &jailed
	}
	return view
}

func findSigning(raw []byte, pages [][]byte) (bool, int64, bool, error) {
	if len(pages) == 0 {
		return false, 0, false, fmt.Errorf("signing infos: empty response")
	}
	for _, body := range pages {
		found, missed, tomb, err := matchSigningPage(raw, body)
		if err != nil {
			return false, 0, false, err
		}
		if found {
			return true, missed, tomb, nil
		}
	}
	return false, 0, false, nil
}

func findJailed(raw []byte, pages [][]byte) (bool, bool, error) {
	if len(pages) == 0 {
		return false, false, fmt.Errorf("staking validators: empty response")
	}
	for _, body := range pages {
		found, jailed, err := matchStakingPage(raw, body)
		if err != nil {
			return false, false, err
		}
		if found {
			return true, jailed, nil
		}
	}
	return false, false, nil
}

func matchSigningPage(raw []byte, body []byte) (bool, int64, bool, error) {
	var page struct {
		Info []struct {
			Address             string `json:"address"`
			MissedBlocksCounter string `json:"missed_blocks_counter"`
			Tombstoned          bool   `json:"tombstoned"`
		} `json:"info"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return false, 0, false, fmt.Errorf("signing infos: decode the response")
	}
	if page.Code != 0 {
		return false, 0, false, fmt.Errorf("signing infos: %s", page.Message)
	}
	for _, info := range page.Info {
		addr, err := decodeConsAddress(info.Address)
		if err != nil || len(addr) != len(raw) {
			continue
		}
		match := true
		for i := range raw {
			if addr[i] != raw[i] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		missed, err := strconv.ParseInt(info.MissedBlocksCounter, 10, 64)
		if err != nil || missed < 0 {
			return false, 0, false, fmt.Errorf("signing infos: missed_blocks_counter is not a count")
		}
		return true, missed, info.Tombstoned, nil
	}
	return false, 0, false, nil
}

func matchStakingPage(raw []byte, body []byte) (bool, bool, error) {
	var page struct {
		Validators []struct {
			Jailed          bool            `json:"jailed"`
			ConsensusPubKey json.RawMessage `json:"consensus_pubkey"`
		} `json:"validators"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return false, false, fmt.Errorf("staking validators: decode the response")
	}
	if page.Code != 0 {
		return false, false, fmt.Errorf("staking validators: %s", page.Message)
	}
	for _, val := range page.Validators {
		pub, err := consensusPubKey(val.ConsensusPubKey)
		if err != nil || len(pub) != 32 {
			continue
		}
		sum := sha256.Sum256(pub)
		match := true
		for i := range raw {
			if sum[i] != raw[i] {
				match = false
				break
			}
		}
		if match {
			return true, val.Jailed, nil
		}
	}
	return false, false, nil
}

func consensusPubKey(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("no pubkey")
	}
	var key struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil, err
	}
	encoded := key.Key
	if encoded == "" {
		encoded = key.Value
	}
	if encoded == "" {
		return nil, fmt.Errorf("no pubkey")
	}
	if pub, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return pub, nil
	}
	return base64.RawStdEncoding.DecodeString(encoded)
}

func parseSlashingParams(body []byte) (int64, float64, error) {
	var page struct {
		Params struct {
			SignedBlocksWindow json.RawMessage `json:"signed_blocks_window"`
			MinSignedPerWindow json.RawMessage `json:"min_signed_per_window"`
		} `json:"params"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return 0, 0, fmt.Errorf("slashing params: decode the response")
	}
	if page.Code != 0 {
		return 0, 0, fmt.Errorf("slashing params: %s", page.Message)
	}
	window, err := parseJSONInt(page.Params.SignedBlocksWindow)
	if err != nil {
		return 0, 0, fmt.Errorf("slashing params: signed_blocks_window")
	}
	minSigned, err := parseJSONFloat(page.Params.MinSignedPerWindow)
	if err != nil {
		return 0, 0, fmt.Errorf("slashing params: min_signed_per_window")
	}
	return window, minSigned, nil
}

func parseJSONInt(raw json.RawMessage) (int64, error) {
	s, err := jsonScalar(raw)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

func parseJSONFloat(raw json.RawMessage) (float64, error) {
	s, err := jsonScalar(raw)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}

func jsonScalar(raw json.RawMessage) (string, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return "", fmt.Errorf("missing")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return string(raw), nil
}

func pageNextKey(body []byte) (string, error) {
	var page struct {
		Pagination struct {
			NextKey *string `json:"next_key"`
		} `json:"pagination"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return "", fmt.Errorf("pagination: decode the response")
	}
	if page.Code != 0 {
		return "", fmt.Errorf("pagination: %s", page.Message)
	}
	if page.Pagination.NextKey == nil {
		return "", nil
	}
	return *page.Pagination.NextKey, nil
}
