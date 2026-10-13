package indexer

import "net/http"

func (a *API) serveEpochRewards(w http.ResponseWriter, r *http.Request, raw string) {
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	epoch, ok := parseEpoch(w, raw)
	if !ok {
		return
	}
	rows, recorded, found, err := a.store.EpochRewards(epoch, page, limit)
	answer(w, map[string]any{"epoch": epoch, "recorded": recorded, "page": page, "limit": limit, "rewards": rows}, found, err)
}

// serveAccountRewards answers a page of what an account was credited, newest epoch first. The list
// grows with the validators that credit the account, so it is read by page number only up to
// MaxListPage and past that by cursor (RewardCursor).
func (a *API) serveAccountRewards(w http.ResponseWriter, r *http.Request, addr string) {
	page, limit, after, ok := keyedQuery(w, r, ParseRewardCursor)
	if !ok {
		return
	}
	if !IsAccountAddress(addr) {
		writeError(w, http.StatusBadRequest, "address must be a lowercase orama account address")
		return
	}
	var from uint64
	var rows []AccountReward
	var err error
	if after != nil {
		from, rows, err = a.store.AccountRewardsAfter(addr, *after, limit)
	} else {
		from, rows, err = a.store.AccountRewards(addr, page, limit)
	}
	if err != nil {
		internalError(w, err)
		return
	}
	next := ""
	if len(rows) > 0 && len(rows) == limit {
		last := rows[len(rows)-1]
		next = RewardCursor{Epoch: last.Epoch, Validator: last.Validator}.String()
	}
	body := map[string]any{"address": addr, "recorded_from_epoch": from}
	writeJSON(w, keyedAnswer(body, "rewards", rows, page, limit, after != nil, next))
}
