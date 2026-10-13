// Command function is the fixture the relayed-fetch feature deploys with
// `orama function deploy`: TinyGo, WASI, stateless. It mints fetch
// capabilities for a CID of its namespace and returns the host's JSON.
package main

import (
	"encoding/json"
	"io"
	"os"
	"unsafe"
)

//go:wasmimport env storage_fetch_cap_mint
func storageFetchCapMint(cidPtr, cidLen uint32, count int32, ttlSeconds int64) uint64

type request struct {
	CID   string `json:"cid"`
	Count int32  `json:"count"`
	TTL   int64  `json:"ttl"`
}

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		reply(`{"error":"read input"}`)
		return
	}
	var req request
	if err := json.Unmarshal(in, &req); err != nil {
		reply(`{"error":"input is not JSON"}`)
		return
	}
	minted := unpack(storageFetchCapMint(ptr(req.CID), uint32(len(req.CID)), req.Count, req.TTL))
	if minted == "" {
		reply(`{"error":"mint refused"}`)
		return
	}
	reply(minted)
}

func reply(s string) { os.Stdout.Write([]byte(s)) }

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
