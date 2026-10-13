// hello greets whoever calls it, and says who that is. It reads the caller with
// the get_caller_wallet host function: the wallet of a signed-in caller, a
// namespace pseudo-id for an API key, nothing for an anonymous one.
package main

import (
	"encoding/json"
	"strings"

	"orama-demo/host"
)

const (
	// defaultName greets a caller who gave no name.
	defaultName = "World"
	// maxNameRunes bounds the name echoed back.
	maxNameRunes = 64
	// anonymous is what the caller is called when the platform knows no one.
	anonymous = "anonymous"
)

type request struct {
	Name string `json:"name"`
}

type response struct {
	Greeting string `json:"greeting"`
	Caller   string `json:"caller"`
}

type problem struct {
	Error string `json:"error"`
}

func main() {
	h := host.New()
	host.Run(func(input []byte) ([]byte, error) { return handle(h, input) })
}

// handle answers one invocation. Input that is not JSON is the caller's mistake
// and is answered as one.
func handle(h host.Host, input []byte) ([]byte, error) {
	var req request
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &req); err != nil {
			return json.Marshal(problem{Error: "the input is not JSON like {\"name\": \"Ada\"}"})
		}
	}
	caller := h.CallerWallet()
	if caller == "" {
		caller = anonymous
	}
	h.LogInfo("hello for " + caller)
	return json.Marshal(response{Greeting: "Hello, " + cleanName(req.Name) + "!", Caller: caller})
}

// cleanName is the name to greet: trimmed, without control characters, at most
// maxNameRunes long, or the default when nothing is left.
func cleanName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if r < ' ' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if len([]rune(b.String())) == maxNameRunes {
			break
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		return s
	}
	return defaultName
}
