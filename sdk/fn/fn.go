// Package fn provides a tiny, TinyGo-compatible SDK for writing Orama serverless functions.
//
// A function is a Go program that reads JSON input from stdin and writes JSON output to stdout.
// This package handles the boilerplate so you only write your handler logic.
//
// Example:
//
//	package main
//
//	import "github.com/DeBrosOfficial/network/sdk/fn"
//
//	func main() {
//		fn.Run(func(input []byte) ([]byte, error) {
//			var req struct{ Name string `json:"name"` }
//			fn.ParseJSON(input, &req)
//			if req.Name == "" { req.Name = "World" }
//			return fn.JSON(map[string]string{"greeting": "Hello, " + req.Name + "!"})
//		})
//	}
package fn

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// HandlerFunc is the signature for a serverless function handler.
// It receives the raw JSON input bytes and returns raw JSON output bytes.
type HandlerFunc func(input []byte) (output []byte, err error)

// Run reads input from stdin, calls the handler, and writes the output to stdout.
// If the handler returns an error, it writes a JSON error response to stdout and exits with code 1.
func Run(handler HandlerFunc) {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError(fmt.Sprintf("failed to read input: %v", err))
		os.Exit(1)
	}

	output, err := handler(input)
	if err != nil {
		writeError(err.Error())
		os.Exit(1)
	}

	if output != nil {
		os.Stdout.Write(output)
	}
}

// JSON marshals a value to JSON bytes. Convenience wrapper around json.Marshal.
func JSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// ParseJSON unmarshals JSON bytes into a value. Convenience wrapper around json.Unmarshal.
func ParseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func writeError(msg string) {
	resp, _ := json.Marshal(map[string]string{"error": msg})
	os.Stdout.Write(resp)
}
