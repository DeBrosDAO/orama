// Example: Counter function with Olric cache
// This function demonstrates using the distributed cache to maintain state.
// Compile with: tinygo build -o counter.wasm -target wasi main.go
//
// Note: This example shows the CONCEPT. Actual host function integration
// requires the host function bindings to be exposed to the WASM module.
package main

import (
	"encoding/json"
	"os"
)

func main() {
	// Read input from stdin
	var input []byte
	buf := make([]byte, 1024)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			input = append(input, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	// Parse input
	var payload struct {
		Action    string `json:"action"`    // "increment", "decrement", "get", "reset"
		CounterID string `json:"counter_id"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		response := map[string]interface{}{
			"error": "Invalid JSON input",
		}
		output, _ := json.Marshal(response)
		os.Stdout.Write(output)
		return
	}

	if payload.CounterID == "" {
		payload.CounterID = "default"
	}

	// NOTE: In the real implementation, this would use host functions:
	// - cache_get(key) to read the counter
	// - cache_put(key, value, ttl) to write the counter
	//
	// For this example, we just simulate the logic:
	response := map[string]interface{}{
		"counter_id": payload.CounterID,
		"action":     payload.Action,
		"message":    "Counter operations require cache host functions",
		"example": map[string]interface{}{
			"increment": "cache_put('counter:' + counter_id, current + 1)",
			"decrement": "cache_put('counter:' + counter_id, current - 1)",
			"get":       "cache_get('counter:' + counter_id)",
			"reset":     "cache_put('counter:' + counter_id, 0)",
		},
	}

	output, _ := json.Marshal(response)
	os.Stdout.Write(output)
}

