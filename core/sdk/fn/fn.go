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
//
// # Host functions and WASM imports
//
// Functions written using only this SDK do NOT need any //go:wasmimport
// directives — input flows through stdin and output through stdout, both
// of which the runtime provides via WASI.
//
// If you need direct access to host functions (db_query, pubsub_publish,
// http_fetch, etc.) declare them via //go:wasmimport. The CANONICAL host
// module name is "env":
//
//	//go:wasmimport env db_query
//	func dbQuery(queryPtr, queryLen, argsPtr, argsLen uint32) uint64
//
// For backward compatibility, the runtime ALSO exposes the same export
// set under the names "host" and "orama". You may use any of the three
// interchangeably — they resolve to identical function tables. New code
// should prefer "env" because it matches the WASI/TinyGo convention and
// what every example in this SDK uses.
//
//	//go:wasmimport env   db_query  // canonical (preferred)
//	//go:wasmimport host  db_query  // alias, supported indefinitely
//	//go:wasmimport orama db_query  // alias, supported indefinitely
//
// All three names produce identical runtime behavior. If you see the
// runtime error `module[X] not instantiated`, your function imported
// from a name other than the three above — fix the directive.
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
