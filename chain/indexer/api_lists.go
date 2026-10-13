package indexer

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
)

// listParams are the parameters of a paged list: page or cursor, limit, and, for the transaction
// lists, a filter. cursor is nil when the list is read by page number.
type listParams struct {
	page, limit int
	filter      string
	cursor      *Cursor
}

// listQuery accepts page, cursor, limit and, if filtered, filter, each at most once, and not page
// with cursor. page is 1 to MaxListPage, cursor one that ParseCursor accepts, limit 1 to MaxLimit,
// filter one of the list filters.
func listQuery(w http.ResponseWriter, r *http.Request, filtered bool) (listParams, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad query")
		return listParams{}, false
	}
	p := listParams{page: defaultPage, limit: DefaultLimit, filter: FilterAll}
	for k, vs := range q {
		if len(vs) != 1 {
			writeError(w, http.StatusBadRequest, "repeated query parameter "+k)
			return listParams{}, false
		}
		if !p.set(k, vs[0], filtered) {
			writeError(w, http.StatusBadRequest, "bad query parameter "+k)
			return listParams{}, false
		}
	}
	if _, paged := q["page"]; paged && p.cursor != nil {
		writeError(w, http.StatusBadRequest, "page and cursor cannot be used together")
		return listParams{}, false
	}
	return p, true
}

// set takes one parameter; it reports false for a name this list does not take or a value out of range.
func (p *listParams) set(key, value string, filtered bool) bool {
	switch {
	case key == "filter" && filtered:
		p.filter = value
		return IsListFilter(value)
	case key == CursorParam:
		c, err := ParseCursor(value)
		p.cursor = &c
		return err == nil
	case key == "page" || key == "limit":
		max := MaxLimit
		if key == "page" {
			max = MaxListPage
		}
		n, ok := boundedInt(value, max)
		if key == "page" {
			p.page = n
		} else {
			p.limit = n
		}
		return ok
	}
	return false
}

// serveLatestTxs answers a page of the transactions, newest first, all of them or those that match
// a filter.
func (a *API) serveLatestTxs(w http.ResponseWriter, r *http.Request) {
	p, ok := listQuery(w, r, true)
	if !ok {
		return
	}
	var txs []Tx
	var err error
	if p.cursor != nil {
		txs, err = a.store.TxsFilteredAfter(p.filter, *p.cursor, p.limit)
	} else {
		txs, err = a.store.TxsFiltered(p.filter, p.page, p.limit)
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, p.answer("txs", txs, txNext(txs, p.limit)))
}

func (a *API) serveAccountTxs(w http.ResponseWriter, r *http.Request, addr string) {
	p, ok := listQuery(w, r, true)
	if !ok {
		return
	}
	if !IsAccountAddress(addr) {
		writeError(w, http.StatusBadRequest, "address must be a lowercase orama account address")
		return
	}
	var txs []Tx
	var err error
	if p.cursor != nil {
		txs, err = a.store.AccountTxsFilteredAfter(addr, p.filter, *p.cursor, p.limit)
	} else {
		txs, err = a.store.AccountTxsFiltered(addr, p.filter, p.page, p.limit)
	}
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, p.answer("txs", txs, txNext(txs, p.limit)))
}

// serveBlocks answers a page of the blocks, newest first, without their transaction hashes.
func (a *API) serveBlocks(w http.ResponseWriter, r *http.Request) {
	p, ok := listQuery(w, r, false)
	if !ok {
		return
	}
	if p.cursor != nil && p.cursor.Index != 0 {
		writeError(w, http.StatusBadRequest, "bad query parameter cursor")
		return
	}
	var blocks []BlockSummary
	var err error
	if p.cursor != nil {
		blocks, err = a.store.BlocksAfter(*p.cursor, p.limit)
	} else {
		blocks, err = a.store.Blocks(p.page, p.limit)
	}
	if err != nil {
		internalError(w, err)
		return
	}
	next := ""
	if len(blocks) == p.limit {
		next = Cursor{Height: blocks[len(blocks)-1].Height}.String()
	}
	body := p.answer("blocks", blocks, next)
	delete(body, "filter")
	writeJSON(w, body)
}

// txNext is the cursor of the page after txs, or "" when the page is short and so the last.
func txNext(txs []Tx, limit int) string {
	if len(txs) == 0 || len(txs) != limit {
		return ""
	}
	last := txs[len(txs)-1]
	return Cursor{Height: last.Height, Index: last.Index}.String()
}

// answer is the body of a list: the rows under name, how they were asked for (page, or nothing for
// a cursor), the filter, and next_cursor when a full page may be followed by more.
func (p listParams) answer(name string, rows any, next string) map[string]any {
	body := map[string]any{"limit": p.limit, "filter": p.filter, name: rows}
	if p.cursor == nil {
		body["page"] = p.page
	}
	if next != "" {
		body["next_cursor"] = next
	}
	return body
}

// keyedQuery is listQuery for the lists paged by the key of their last row (cursor_keys.go): it
// accepts page, cursor and limit, each at most once, and not page with cursor. page is 1 to
// MaxListPage, limit 1 to MaxLimit, and cursor one that parse accepts; cursor is nil when the list
// is read by page number.
func keyedQuery[C any](w http.ResponseWriter, r *http.Request, parse func(string) (C, error)) (page, limit int, cursor *C, ok bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad query")
		return 0, 0, nil, false
	}
	page, limit = defaultPage, DefaultLimit
	for k, vs := range q {
		if len(vs) != 1 {
			writeError(w, http.StatusBadRequest, "repeated query parameter "+k)
			return 0, 0, nil, false
		}
		if !setKeyed(k, vs[0], parse, &page, &limit, &cursor) {
			writeError(w, http.StatusBadRequest, "bad query parameter "+k)
			return 0, 0, nil, false
		}
	}
	if _, paged := q["page"]; paged && cursor != nil {
		writeError(w, http.StatusBadRequest, "page and cursor cannot be used together")
		return 0, 0, nil, false
	}
	return page, limit, cursor, true
}

// setKeyed takes one parameter of keyedQuery; it reports false for a name the list does not take or
// a value out of range.
func setKeyed[C any](key, value string, parse func(string) (C, error), page, limit *int, cursor **C) bool {
	switch key {
	case CursorParam:
		c, err := parse(value)
		*cursor = &c
		return err == nil
	case "page":
		n, good := boundedInt(value, MaxListPage)
		*page = n
		return good
	case "limit":
		n, good := boundedInt(value, MaxLimit)
		*limit = n
		return good
	}
	return false
}

// keyedAnswer is the body of a keyed list: body, plus the rows under name, the limit, the page when
// the list was read by page number, and next_cursor when a full page may be followed by more.
func keyedAnswer(body map[string]any, name string, rows any, page, limit int, byCursor bool, next string) map[string]any {
	body[name], body["limit"] = rows, limit
	if !byCursor {
		body["page"] = page
	}
	if next != "" {
		body["next_cursor"] = next
	}
	return body
}

// serveHome answers the snapshot the home page shows, reused for HeadMaxAge seconds while the index stays at one block.
func (a *API) serveHome(w http.ResponseWriter, r *http.Request) {
	if !noQuery(w, r) {
		return
	}
	st, err := a.store.Status()
	if err != nil {
		internalError(w, err)
		return
	}
	body, err := a.home.get(r.Context(), st.Cursor, a.now, a.buildHome)
	var chainErr chainReadError
	switch {
	case errors.As(err, &chainErr):
		slog.Error("home snapshot: chain unreachable", "err", err)
		writeError(w, http.StatusBadGateway, "chain unreachable")
		return
	case err != nil:
		internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(body); err != nil {
		slog.Error("failed to write the home snapshot", "err", err)
	}
}
