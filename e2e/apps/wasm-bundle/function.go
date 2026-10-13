// Command function is the reference WASM function bundle (TinyGo, WASI): one
// source deployed as several small functions, each told its job by the
// APP_ROLE entry of its function.yaml env.
//
//	store  a per-owner item store on the namespace's RQLite, counting writes in the cache
//	chat   send a chat message (as the calling wallet) and history of a room
//	relay  pubsub trigger: push the named user through the namespace's push, and record it
//	cron   cron trigger: record one row per fire, bucketed by minute
//	fetch  http_fetch a public URL and report the status
//
// Every reply is JSON on stdout.
package main

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
)

type request struct {
	Op     string          `json:"op"`
	Owner  string          `json:"owner"`
	Room   string          `json:"room"`
	Text   string          `json:"text"`
	Kind   string          `json:"kind"`
	URL    string          `json:"url"`
	Notify string          `json:"notify"`
	Topic  string          `json:"topic"`
	Data   json.RawMessage `json:"data"`
	Depth  int             `json:"trigger_depth"`
}

const (
	itemsDDL = `CREATE TABLE IF NOT EXISTS ref_items (id INTEGER PRIMARY KEY, owner TEXT NOT NULL, room TEXT NOT NULL DEFAULT '', text TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`
	firesDDL = `CREATE TABLE IF NOT EXISTS ref_fires (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, slot TEXT NOT NULL, marker TEXT NOT NULL DEFAULT '', depth INTEGER NOT NULL DEFAULT 0, request_id TEXT NOT NULL DEFAULT '')`
	// writesKey counts store writes in the namespace's function cache.
	writesKey = "ref:writes"
)

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		reply(map[string]any{"error": "read input: " + err.Error()})
		return
	}
	var req request
	if len(in) > 0 {
		if err := json.Unmarshal(in, &req); err != nil {
			reply(map[string]any{"error": "input is not JSON: " + err.Error()})
			return
		}
	}
	switch role := env("APP_ROLE"); role {
	case "store":
		reply(store(req))
	case "chat":
		reply(chat(req))
	case "relay":
		reply(relay(req))
	case "cron":
		reply(fire("cron", "", req.Depth))
	case "fetch":
		reply(fetch(req.URL))
	default:
		reply(map[string]any{"error": "unknown APP_ROLE " + strconv.Quote(role)})
	}
}

func store(req request) any {
	exec(itemsDDL)
	switch req.Op {
	case "put":
		res := raw(exec(`INSERT INTO ref_items (owner, text) VALUES (?, ?)`, req.Owner, req.Text))
		n := cacheIncrBy(ptr(writesKey), size(writesKey), 1)
		return map[string]any{"result": res, "writes": n}
	case "list":
		return map[string]any{"result": raw(query(`SELECT id, text FROM ref_items WHERE owner = ? ORDER BY id`, req.Owner))}
	case "count":
		return map[string]any{"result": raw(query(`SELECT COUNT(*) AS n FROM ref_items`))}
	case "fires":
		exec(firesDDL)
		return map[string]any{"result": raw(query(`SELECT slot, marker, COUNT(*) AS n FROM ref_fires WHERE kind = ? GROUP BY slot, marker ORDER BY slot`, req.Kind))}
	}
	return map[string]any{"error": "unknown store op " + strconv.Quote(req.Op)}
}

// chat sends as the wallet the gateway authenticated, never as one the
// input names, and fans the message out on the room's topic.
func chat(req request) any {
	exec(itemsDDL)
	from := unpack(getCallerWallet())
	switch req.Op {
	case "send":
		res := raw(exec(`INSERT INTO ref_items (owner, room, text) VALUES (?, ?, ?)`, from, req.Room, req.Text))
		msg, _ := json.Marshal(map[string]string{"from": from, "text": req.Text})
		topic := "chat." + req.Room
		ok := pubsubPublish(ptr(topic), size(topic), ptr(string(msg)), size(string(msg)))
		if req.Notify != "" {
			note, _ := json.Marshal(map[string]string{"user": req.Notify, "marker": req.Text})
			pubsubPublish(ptr("chat-notify"), size("chat-notify"), ptr(string(note)), size(string(note)))
		}
		return map[string]any{"result": res, "from": from, "published": ok}
	case "history":
		return map[string]any{"result": raw(query(`SELECT owner, text FROM ref_items WHERE room = ? ORDER BY id`, req.Room))}
	}
	return map[string]any{"error": "unknown chat op " + strconv.Quote(req.Op)}
}

// relay is a pubsub trigger: data names the user to push and a marker.
func relay(req request) any {
	var ev struct {
		User   string `json:"user"`
		Marker string `json:"marker"`
	}
	if err := json.Unmarshal(req.Data, &ev); err != nil || ev.User == "" {
		return map[string]any{"error": "trigger data is not {user, marker}"}
	}
	msg, _ := json.Marshal(map[string]string{"title": "New message", "body": ev.Marker})
	pushed := unpack(pushSendV2(ptr(ev.User), size(ev.User), ptr(string(msg)), size(string(msg))))
	logInfo(ptr("relay pushed "+ev.Marker), size("relay pushed "+ev.Marker))
	return map[string]any{"fire": fire("relay", ev.Marker, req.Depth), "push": raw(pushed)}
}

// fire records one trigger fire, bucketed by the minute it ran in.
func fire(kind, marker string, depth int) any {
	exec(firesDDL)
	reqID := unpack(getRequestID())
	return raw(exec(`INSERT INTO ref_fires (kind, slot, marker, depth, request_id) VALUES (?, strftime('%Y-%m-%d %H:%M', 'now'), ?, ?, ?)`, kind, marker, depth, reqID))
}

func fetch(url string) any {
	const method = "GET"
	headers := `{"User-Agent":"orama-e2e-reference"}`
	out := unpack(httpFetch(ptr(method), size(method), ptr(url), size(url), ptr(headers), size(headers), 0, 0))
	var env struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		return map[string]any{"error": "http_fetch envelope is not JSON"}
	}
	return map[string]any{"status": env.Status, "bytes": len(env.Body), "error": env.Error}
}

func jsonArgs(args []any) string {
	if len(args) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(args)
	return string(b)
}

func raw(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		b, _ := json.Marshal(s)
		return b
	}
	return json.RawMessage(s)
}

func reply(v any) {
	out, err := json.Marshal(v)
	if err != nil {
		out = []byte(`{"error":"encode reply"}`)
	}
	os.Stdout.Write(out)
}
