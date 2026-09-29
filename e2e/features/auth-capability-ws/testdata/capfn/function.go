// Command function is the capability fixture the auth-capability-ws feature
// deploys with `orama function deploy`: TinyGo, WASI, stateless. It mints and
// revokes capabilities for its own socket, and on any other input reports
// the capability the caller's socket was opened with.
package main

import (
	"encoding/json"
	"io"
	"os"
	"unsafe"
)

//go:wasmimport env capability_mint
func capabilityMint(resPtr, resLen uint32, ttlSeconds int64) uint64

//go:wasmimport env capability_revoke
func capabilityRevoke(tokenPtr, tokenLen uint32) uint32

//go:wasmimport env get_caller_capability
func getCallerCapability() uint64

type request struct {
	Op       string `json:"op"`
	Resource string `json:"resource"`
	TTL      int64  `json:"ttl"`
	Token    string `json:"token"`
}

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
	switch req.Op {
	case "mint":
		minted := unpack(capabilityMint(ptr(req.Resource), uint32(len(req.Resource)), req.TTL))
		if minted == "" {
			reply(map[string]any{"error": "mint refused"})
			return
		}
		os.Stdout.Write([]byte(minted))
	case "revoke":
		reply(map[string]any{"revoked": capabilityRevoke(ptr(req.Token), uint32(len(req.Token))) == 1})
	default:
		reply(map[string]any{"capability": unpack(getCallerCapability())})
	}
}

func reply(v map[string]any) {
	out, err := json.Marshal(v)
	if err != nil {
		out = []byte(`{"error":"encode reply"}`)
	}
	os.Stdout.Write(out)
}

// ptr is the guest address of s's bytes; 0 for an empty string.
func ptr(s string) uint32 {
	if s == "" {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.StringData(s))))
}

// unpack reads the host's packed ptr<<32|len result; 0 is "nothing".
func unpack(v uint64) string {
	if v == 0 {
		return ""
	}
	p, n := uint32(v>>32), uint32(v)
	return string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(p))), n))
}
