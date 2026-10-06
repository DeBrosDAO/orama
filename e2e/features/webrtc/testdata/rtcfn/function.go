// Command function is the WebRTC fixture the webrtc feature deploys with
// `orama function deploy`: TinyGo, WASI, stateless. It admits a user to a room,
// kicks one and mutes one through the webrtc_* host calls, and reports what the
// host answered.
package main

import (
	"encoding/json"
	"io"
	"os"
	"unsafe"
)

//go:wasmimport env webrtc_admit
func webrtcAdmit(roomPtr, roomLen, userPtr, userLen, devPtr, devLen uint32, ttlSeconds int64) uint64

//go:wasmimport env webrtc_kick
func webrtcKick(roomPtr, roomLen, userPtr, userLen uint32) uint32

//go:wasmimport env webrtc_mute
func webrtcMute(roomPtr, roomLen, userPtr, userLen, muted uint32) uint32

type request struct {
	Op     string `json:"op"`
	Room   string `json:"room"`
	User   string `json:"user"`
	Device string `json:"device"`
	TTL    int64  `json:"ttl"`
	Muted  bool   `json:"muted"`
}

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		reply(map[string]any{"error": "read input: " + err.Error()})
		return
	}
	var req request
	if err := json.Unmarshal(in, &req); err != nil {
		reply(map[string]any{"error": "input is not JSON: " + err.Error()})
		return
	}
	switch req.Op {
	case "admit":
		admitted := unpack(webrtcAdmit(ptr(req.Room), uint32(len(req.Room)), ptr(req.User), uint32(len(req.User)),
			ptr(req.Device), uint32(len(req.Device)), req.TTL))
		if admitted == "" {
			reply(map[string]any{"error": "admit refused"})
			return
		}
		os.Stdout.Write([]byte(admitted))
	case "kick":
		reply(map[string]any{"done": webrtcKick(ptr(req.Room), uint32(len(req.Room)), ptr(req.User), uint32(len(req.User))) == 1})
	case "mute":
		var m uint32
		if req.Muted {
			m = 1
		}
		reply(map[string]any{"done": webrtcMute(ptr(req.Room), uint32(len(req.Room)), ptr(req.User), uint32(len(req.User)), m) == 1})
	default:
		reply(map[string]any{"error": "unknown op " + req.Op})
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
