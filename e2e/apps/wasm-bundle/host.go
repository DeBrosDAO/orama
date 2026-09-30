package main

import "unsafe"

// Host functions the reference functions use (docs/SERVERLESS.md
// "Host Functions API"), from the canonical module env.

//go:wasmimport env get_env
func getEnv(p, n uint32) uint64

//go:wasmimport env get_caller_wallet
func getCallerWallet() uint64

//go:wasmimport env get_request_id
func getRequestID() uint64

//go:wasmimport env db_query_v2
func dbQueryV2(sp, sn, ap, an uint32) uint64

//go:wasmimport env db_execute_v2
func dbExecuteV2(sp, sn, ap, an uint32) uint64

//go:wasmimport env cache_incr_by
func cacheIncrBy(p, n uint32, delta int64) int64

//go:wasmimport env http_fetch
func httpFetch(mp, mn, up, un, hp, hn, bp, bn uint32) uint64

//go:wasmimport env pubsub_publish
func pubsubPublish(tp, tn, dp, dn uint32) uint32

//go:wasmimport env push_send_v2
func pushSendV2(up, un, mp, mn uint32) uint64

//go:wasmimport env log_info
func logInfo(p, n uint32)

// ptr is the guest address of s's bytes; 0 for an empty string.
func ptr(s string) uint32 {
	if s == "" {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.StringData(s))))
}

func size(s string) uint32 { return uint32(len(s)) }

// unpack reads the host's packed ptr<<32|len result; 0 is "nothing".
func unpack(v uint64) string {
	if v == 0 {
		return ""
	}
	p, n := uint32(v>>32), uint32(v)
	return string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(p))), n))
}

func env(key string) string { return unpack(getEnv(ptr(key), size(key))) }

// exec runs a write with JSON args and returns the host's JSON envelope.
func exec(sql string, args ...any) string {
	a := jsonArgs(args)
	return unpack(dbExecuteV2(ptr(sql), size(sql), ptr(a), size(a)))
}

// query runs a read with JSON args and returns the host's JSON envelope.
func query(sql string, args ...any) string {
	a := jsonArgs(args)
	return unpack(dbQueryV2(ptr(sql), size(sql), ptr(a), size(a)))
}
