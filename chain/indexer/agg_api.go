package indexer

import (
	"net/http"
	"strconv"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// serveAggregates answers the routes over the aggregates the follower stores: epochs, supply,
// validators and the long statistics series. It reports false for a path that is none of them.
func (a *API) serveAggregates(w http.ResponseWriter, r *http.Request, segs []string) bool {
	switch {
	case len(segs) == 1 && segs[0] == "epochs":
		a.serveEpochs(w, r)
	case len(segs) == 2 && segs[0] == "epochs":
		a.serveEpoch(w, r, segs[1])
	case len(segs) == 3 && segs[0] == "epochs" && segs[2] == "validators":
		a.serveEpochValidators(w, r, segs[1])
	case len(segs) == 1 && segs[0] == "supply":
		a.serveSupply(w, r)
	case len(segs) == 1 && segs[0] == "validators":
		a.serveValidators(w, r)
	case len(segs) == 2 && segs[0] == "validators":
		a.serveValidator(w, r, segs[1])
	case len(segs) == 3 && segs[0] == "validators":
		return a.serveValidatorList(w, r, segs[1], segs[2])
	case len(segs) == 2 && segs[0] == "stats":
		return a.serveSeries(w, r, segs[1])
	default:
		return false
	}
	return true
}

func (a *API) serveEpochs(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	rows, err := a.store.Epochs(page, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"page": page, "limit": limit, "epochs": rows})
}

func (a *API) serveEpoch(w http.ResponseWriter, r *http.Request, raw string) {
	if !noQuery(w, r) {
		return
	}
	epoch, ok := parseEpoch(w, raw)
	if !ok {
		return
	}
	row, found, err := a.store.Epoch(epoch)
	answer(w, row, found, err)
}

func (a *API) serveEpochValidators(w http.ResponseWriter, r *http.Request, raw string) {
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	epoch, ok := parseEpoch(w, raw)
	if !ok {
		return
	}
	rows, found, err := a.store.EpochValidators(epoch, page, limit)
	answer(w, map[string]any{"epoch": epoch, "page": page, "limit": limit, "validators": rows}, found, err)
}

func (a *API) serveSupply(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	points, err := a.store.Supply(page, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"page": page, "limit": limit, "points": points})
}

func (a *API) serveValidators(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	rows, err := a.store.Validators(page, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"page": page, "limit": limit, "validators": rows})
}

func (a *API) serveValidator(w http.ResponseWriter, r *http.Request, operator string) {
	if !noQuery(w, r) {
		return
	}
	if !validOperator(w, operator) {
		return
	}
	v, found, err := a.store.Validator(operator)
	answer(w, v, found, err)
}

// serveValidatorList answers a validator's epochs, slashes or jail periods, a page at a time.
func (a *API) serveValidatorList(w http.ResponseWriter, r *http.Request, operator, kind string) bool {
	var list func(string, int, int) (any, bool, error)
	switch kind {
	case "epochs":
		list = func(op string, p, l int) (any, bool, error) { return a.store.ValidatorEpochs(op, p, l) }
	case "slashes":
		list = func(op string, p, l int) (any, bool, error) { return a.store.ValidatorSlashes(op, p, l) }
	case "jails":
		list = func(op string, p, l int) (any, bool, error) { return a.store.ValidatorJails(op, p, l) }
	default:
		return false
	}
	page, limit, ok := pageQuery(w, r)
	if !ok || !validOperator(w, operator) {
		return true
	}
	rows, found, err := list(operator, page, limit)
	answer(w, map[string]any{"operator": operator, "page": page, "limit": limit, kind: rows}, found, err)
	return true
}

// seriesIntervals maps a route's name for an interval to the interval.
var seriesIntervals = map[string]string{"hourly": IntervalHour, "daily": IntervalDay, "weekly": IntervalWeek}

func (a *API) serveSeries(w http.ResponseWriter, r *http.Request, name string) bool {
	interval, known := seriesIntervals[name]
	if !known {
		return false
	}
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return true
	}
	buckets, err := a.store.Series(interval, page, limit)
	if err != nil {
		internalError(w, err)
		return true
	}
	writeJSON(w, map[string]any{"interval": interval, "page": page, "limit": limit, "buckets": buckets})
	return true
}

func parseEpoch(w http.ResponseWriter, raw string) (uint64, bool) {
	if !positiveRE.MatchString(raw) {
		writeError(w, http.StatusBadRequest, "epoch must be a positive integer")
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "epoch must be a positive integer")
		return 0, false
	}
	return n, true
}

func validOperator(w http.ResponseWriter, operator string) bool {
	if !IsValidatorAddress(operator) {
		writeError(w, http.StatusBadRequest, "operator must be a lowercase orama validator operator address")
		return false
	}
	return true
}

// IsValidatorAddress reports whether s is a canonical (lowercase) bech32 Orama validator operator
// address.
func IsValidatorAddress(s string) bool {
	if len(s) == 0 || len(s) > maxAddressLen {
		return false
	}
	hrp, raw, err := bech32.DecodeAndConvert(s)
	if err != nil || hrp != params.Bech32PrefixValAddr || len(raw) == 0 || len(raw) > maxAddressBytes {
		return false
	}
	canonical, err := bech32.ConvertAndEncode(hrp, raw)
	return err == nil && canonical == s
}
