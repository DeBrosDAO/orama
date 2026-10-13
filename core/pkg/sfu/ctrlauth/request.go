package ctrlauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	// MACHeader carries "<unix seconds>.<hex nonce>.<hex hmac>" on a control
	// request or a membership event. The nonce makes every stamp unique, so a
	// receiver can refuse a replay (ReplayGuard) without refusing two honest
	// requests with the same body in the same second.
	MACHeader = "X-Orama-SFU-MAC"

	// maxSkew bounds replay of a captured request. Both directions: a stamp
	// from the future is as forged as an old one.
	maxSkew = 60 * time.Second

	// nonceBytes is the size of the random value each stamp carries.
	nonceBytes = 16
)

// ErrBadMAC means a request's stamp is missing, stale, or does not verify.
var ErrBadMAC = errors.New("request is not authentic: the MAC is missing, stale or wrong")

// payload is exactly what a request MAC covers: the target, the method, the
// path, the timestamp and the SHA-256 of the body. The body is covered because
// it carries the parameters (which room, which user, muted or not): a stamp that
// did not would replay onto any other body inside its window. The target is
// covered because every SFU and every gateway of a namespace shares the key and
// keeps its own replay guard: a stamp captured on its way to one would
// otherwise verify, once, at any other.
func payload(target, method, path string, body []byte, ts int64, nonce string) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-sfu-control-v2",
		target,
		strings.ToUpper(method),
		path,
		strconv.FormatInt(ts, 10),
		nonce,
		hex.EncodeToString(sum[:]),
	}, "\n")
}

// Sign returns the MACHeader value for a request, with a fresh nonce. target
// names the receiver as both ends know it unambiguously: for a control request
// the SFU's signalling address (host:port, the SFU's listen address), for a
// membership event the gateway's event sink URL (the one its tickets carry).
func Sign(key []byte, target, method, path string, body []byte, now time.Time) string {
	raw := make([]byte, nonceBytes)
	// Since Go 1.24 rand.Read does not return on failure: it ends the process.
	_, _ = rand.Read(raw)
	ts, nonce := now.Unix(), hex.EncodeToString(raw)
	return strconv.FormatInt(ts, 10) + "." + nonce + "." + hex.EncodeToString(mac(key, payload(target, method, path, body, ts, nonce)))
}

// Verify checks a MACHeader value against the request it came with, as sent to
// target (the receiver's own name for itself, see Sign). It does not
// refuse a replay inside the skew window: the receiver does that with a
// ReplayGuard, once the stamp has proved authentic.
func Verify(key []byte, target, header, method, path string, body []byte, now time.Time) error {
	ts, nonce, sig, ok := splitStamp(header)
	if !ok {
		return ErrBadMAC
	}
	if skew := now.Sub(time.Unix(ts, 0)); skew > maxSkew || skew < -maxSkew {
		return ErrBadMAC
	}
	presented, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(presented, mac(key, payload(target, method, path, body, ts, nonce))) {
		return ErrBadMAC
	}
	return nil
}

// splitStamp parses "<unix seconds>.<nonce>.<hmac>".
func splitStamp(header string) (ts int64, nonce, sig string, ok bool) {
	parts := strings.Split(strings.TrimSpace(header), ".")
	if len(parts) != 3 || parts[1] == "" {
		return 0, "", "", false
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", "", false
	}
	return ts, parts[1], parts[2], true
}
