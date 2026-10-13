//go:build wasip1

package host

import (
	"encoding/json"
	"errors"
	"unsafe"
)

// The host functions the gateway exports to every function, under the module
// name env (core/pkg/serverless/engine.go, registerHostModule). Strings cross
// the boundary as a pointer and a length in the function's memory; results come
// back packed as pointer<<32 | length.

//go:wasmimport env get_caller_wallet
func getCallerWallet() uint64

//go:wasmimport env cache_incr_by
func cacheIncrBy(keyPtr, keyLen uint32, delta int64) int64

//go:wasmimport env db_query_v2
func dbQueryV2(sqlPtr, sqlLen, argsPtr, argsLen uint32) uint64

//go:wasmimport env db_execute_v2
func dbExecuteV2(sqlPtr, sqlLen, argsPtr, argsLen uint32) uint64

//go:wasmimport env log_info
func logInfo(ptr, length uint32)

// wasmHost is the Host over those functions.
type wasmHost struct{}

// New is the Host of a function running in the gateway.
func New() Host { return wasmHost{} }

func (wasmHost) CallerWallet() string { return unpack(getCallerWallet()) }

func (wasmHost) CacheIncrBy(key string, delta int64) int64 {
	return cacheIncrBy(ptr(key), size(key), delta)
}

func (wasmHost) LogInfo(msg string) { logInfo(ptr(msg), size(msg)) }

func (wasmHost) DBQuery(sql string, args ...any) ([]map[string]any, error) {
	argsJSON, err := encodeArgs(args)
	if err != nil {
		return nil, err
	}
	raw := unpack(dbQueryV2(ptr(sql), size(sql), ptr(argsJSON), size(argsJSON)))
	if raw == "" {
		return nil, errors.New("db_query_v2 refused the statement (not allowed, or the database is unreachable)")
	}
	var out struct {
		Rows  []map[string]any `json:"rows"`
		Error string           `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, errors.New("db_query_v2 answered something that is not JSON")
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	return out.Rows, nil
}

func (wasmHost) DBExec(sql string, args ...any) (Result, error) {
	argsJSON, err := encodeArgs(args)
	if err != nil {
		return Result{}, err
	}
	raw := unpack(dbExecuteV2(ptr(sql), size(sql), ptr(argsJSON), size(argsJSON)))
	if raw == "" {
		return Result{}, errors.New("db_execute_v2 refused the statement (not allowed, or the database is unreachable)")
	}
	var out struct {
		Result
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return Result{}, errors.New("db_execute_v2 answered something that is not JSON")
	}
	if out.Error != "" {
		return Result{}, errors.New(out.Error)
	}
	return out.Result, nil
}

// encodeArgs is the arguments as the JSON array the database host functions
// take; no arguments is the empty string, which they read as none.
func encodeArgs(args []any) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	b, err := json.Marshal(args)
	return string(b), err
}

// ptr is the address of s's bytes in this function's memory; 0 for an empty string.
func ptr(s string) uint32 {
	if s == "" {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.StringData(s))))
}

func size(s string) uint32 { return uint32(len(s)) }

// unpack reads the host's packed pointer<<32 | length result; 0 is nothing.
func unpack(v uint64) string {
	if v == 0 {
		return ""
	}
	p, n := uint32(v>>32), uint32(v)
	return string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(p))), n))
}
