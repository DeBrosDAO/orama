// Command function imports a host module the gateway does not register, so
// every invocation fails to instantiate (website/src/docs/developer/functions.mdx#host-functions-api).
package main

import "os"

//go:wasmimport nosuchmodule get_request_id
func getRequestID() uint64

func main() {
	if getRequestID() == 0 {
		os.Stdout.Write([]byte(`{"unreachable":true}`))
	}
}
