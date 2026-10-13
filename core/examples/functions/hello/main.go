// Example: Hello function
// This is a simple serverless function that returns a greeting.
// Compile with: tinygo build -o hello.wasm -target wasi main.go
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

	// Parse input to get name
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &payload); err != nil || payload.Name == "" {
		payload.Name = "World"
	}

	// Create greeting response
	response := map[string]interface{}{
		"greeting": "Hello, " + payload.Name + "!",
		"message":  "This is a serverless function running on Orama Network",
	}

	output, _ := json.Marshal(response)
	os.Stdout.Write(output)
}

