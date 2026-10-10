// guestbook keeps a guestbook in the namespace's own database. Signing it is one
// db_execute_v2 host function call, reading it one db_query_v2 call: the rows live
// in the namespace's replicated RQLite, so every gateway of the namespace shows
// the same book.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"orama-demo/host"
)

const (
	actionList = "list"
	actionSign = "sign"

	maxNameRunes    = 40
	maxMessageRunes = 280
	// listLimit is how many of the newest entries a read returns.
	listLimit = 20
)

// The statements. One call runs one statement, so the table is made by its own
// call. The table is the function's own, and its name avoids the platform's
// reserved ones.
const (
	createTable = `CREATE TABLE IF NOT EXISTS guestbook_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		message TEXT NOT NULL,
		caller TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`
	insertEntry  = `INSERT INTO guestbook_entries (name, message, caller) VALUES (?, ?, ?)`
	selectLatest = `SELECT id, name, message, created_at FROM guestbook_entries ORDER BY id DESC LIMIT ?`
)

type request struct {
	Action  string `json:"action"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type entry struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
	At      string `json:"at"`
}

type response struct {
	Entries []entry `json:"entries"`
	Signed  *entry  `json:"signed,omitempty"`
}

type problem struct {
	Error string `json:"error"`
}

func main() {
	h := host.New()
	host.Run(func(input []byte) ([]byte, error) { return handle(h, input) })
}

// handle answers one invocation: sign the book, or read it.
func handle(h host.Host, input []byte) ([]byte, error) {
	var req request
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &req); err != nil {
			return json.Marshal(problem{Error: "the input is not JSON like {\"action\": \"list\"}"})
		}
	}
	switch req.Action {
	case "", actionList:
		return list(h, nil)
	case actionSign:
		return sign(h, req)
	default:
		return json.Marshal(problem{Error: "action must be \"list\" or \"sign\""})
	}
}

// sign adds an entry to the book and returns the book.
func sign(h host.Host, req request) ([]byte, error) {
	name, message := clean(req.Name, maxNameRunes), clean(req.Message, maxMessageRunes)
	if name == "" || message == "" {
		return json.Marshal(problem{Error: fmt.Sprintf("sign needs a name (up to %d characters) and a message (up to %d)", maxNameRunes, maxMessageRunes)})
	}
	if _, err := h.DBExec(createTable); err != nil {
		return nil, fmt.Errorf("create the guestbook table: %w", err)
	}
	res, err := h.DBExec(insertEntry, name, message, h.CallerWallet())
	if err != nil {
		return nil, fmt.Errorf("sign the guestbook: %w", err)
	}
	h.LogInfo(fmt.Sprintf("guestbook entry %d signed", res.LastInsertID))
	return list(h, &entry{ID: res.LastInsertID, Name: name, Message: message})
}

// list reads the newest entries, newest first. signed, when set, is the entry
// the caller just wrote.
func list(h host.Host, signed *entry) ([]byte, error) {
	if _, err := h.DBExec(createTable); err != nil {
		return nil, fmt.Errorf("create the guestbook table: %w", err)
	}
	rows, err := h.DBQuery(selectLatest, listLimit)
	if err != nil {
		return nil, fmt.Errorf("read the guestbook: %w", err)
	}
	entries := make([]entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, entry{ID: asInt(row["id"]), Name: asString(row["name"]), Message: asString(row["message"]), At: asString(row["created_at"])})
	}
	if signed != nil {
		for _, e := range entries {
			if e.ID == signed.ID {
				signed.At = e.At
			}
		}
	}
	return json.Marshal(response{Entries: entries, Signed: signed})
}

// clean is s trimmed, without control characters, cut to max characters.
func clean(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) {
			continue
		}
		if n == max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// asInt reads a number from a decoded JSON row: the database answers JSON, in
// which every number is a float64.
func asInt(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
