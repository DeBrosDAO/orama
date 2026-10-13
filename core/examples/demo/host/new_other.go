//go:build !wasip1

package host

// New is the Host a function runs on when it is not running in the gateway: an
// empty Fake, so `go build`, `go vet` and `go test` see the same code the WASM
// build compiles.
func New() Host { return NewFake() }
