// Example: Echo function
// This is a simple serverless function that echoes back the input.
// Compile with: tinygo build -o echo.wasm -target wasi main.go
package main

import (
	"encoding/json"
	"os"
)

// Input is read from stdin, output is written to stdout.
// The Orama serverless engine passes the invocation payload via stdin
// and expects the response on stdout.

func main() {
	// Read all input from stdin
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

	// Parse input as JSON (optional - could also just echo raw bytes)
	var payload map[string]interface{}
	if err := json.Unmarshal(input, &payload); err != nil {
		// Not JSON, just echo the raw input
		response := map[string]interface{}{
			"echo": string(input),
		}
		output, _ := json.Marshal(response)
		os.Stdout.Write(output)
		return
	}

	// Create response
	response := map[string]interface{}{
		"echo":    payload,
		"message": "Echo function received your input!",
	}

	output, _ := json.Marshal(response)
	os.Stdout.Write(output)
}

