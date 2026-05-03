// Package sniproxy provides a TCP-level Server Name Indication (SNI) router.
//
// The router peeks at the unencrypted TLS ClientHello on each accepted
// connection, extracts the SNI host name, and forwards the raw stream to
// a backend. It does NOT terminate TLS — encrypted bytes pass through
// verbatim. This lets one TCP port serve multiple TLS-speaking backends
// (HTTPS for the gateway, TURNS for stealth WebRTC, etc.) without
// sharing private keys with the proxy.
//
// Design goals:
//   - Zero TLS material on the proxy
//   - Bounded ClientHello read (no slowloris)
//   - Backend dial timeout
//   - Per-IP rate limiting
//
// SNI parsing follows RFC 5246 §7.4.1.2 (TLS record + ClientHello) and
// RFC 6066 §3 (server_name extension).
package sniproxy

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// ErrNoSNI is returned when the ClientHello has no server_name extension.
var ErrNoSNI = errors.New("sniproxy: ClientHello has no SNI")

// MaxClientHelloBytes bounds how many bytes we'll read while looking for
// the SNI. TLS ClientHello records are typically 200–500 bytes; this is
// a generous cap that still defends against memory abuse.
const MaxClientHelloBytes = 16 * 1024

// PeekClientHello reads bytes from conn until a TLS ClientHello has
// been parsed (or MaxClientHelloBytes is exceeded). Returns the SNI
// hostname (lowercased), the bytes consumed (must be replayed to the
// backend), and any error.
//
// readTimeout bounds the wait — slowloris-style stalls return an error
// quickly without holding the goroutine indefinitely.
func PeekClientHello(conn net.Conn, readTimeout time.Duration) (string, []byte, error) {
	if readTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		defer conn.SetReadDeadline(time.Time{})
	}

	br := bufio.NewReaderSize(conn, MaxClientHelloBytes)

	// Peek the TLS record header (5 bytes): content_type, version (2),
	// length (2). content_type for ClientHello is 22 (handshake).
	header, err := br.Peek(5)
	if err != nil {
		return "", nil, fmt.Errorf("read tls record header: %w", err)
	}
	if header[0] != 22 {
		return "", nil, fmt.Errorf("not a TLS handshake record (type=%d)", header[0])
	}
	recLen := int(binary.BigEndian.Uint16(header[3:5]))
	if recLen <= 0 || 5+recLen > MaxClientHelloBytes {
		return "", nil, fmt.Errorf("invalid record length %d", recLen)
	}

	full, err := br.Peek(5 + recLen)
	if err != nil {
		return "", nil, fmt.Errorf("read tls record body: %w", err)
	}

	sni, err := parseSNI(full[5:])
	if err != nil {
		return "", nil, err
	}

	// We've only peeked — drain the buffer to capture the bytes for replay.
	consumed := make([]byte, br.Buffered())
	if _, err := io.ReadFull(br, consumed); err != nil {
		return "", nil, fmt.Errorf("drain peeked bytes: %w", err)
	}

	return sni, consumed, nil
}

// parseSNI parses a TLS ClientHello body (without the 5-byte record
// header) and returns the server_name extension value if present.
func parseSNI(body []byte) (string, error) {
	r := newReader(body)

	// Handshake type (1 byte) — must be 1 (ClientHello).
	hsType, err := r.readByte()
	if err != nil {
		return "", err
	}
	if hsType != 1 {
		return "", fmt.Errorf("not a ClientHello (handshake type %d)", hsType)
	}
	// Handshake length (3 bytes).
	if _, err := r.readBytes(3); err != nil {
		return "", err
	}
	// client_version (2) + random (32).
	if _, err := r.readBytes(2 + 32); err != nil {
		return "", err
	}
	// session_id.
	sidLen, err := r.readByte()
	if err != nil {
		return "", err
	}
	if _, err := r.readBytes(int(sidLen)); err != nil {
		return "", err
	}
	// cipher_suites length (2).
	csLen, err := r.readUint16()
	if err != nil {
		return "", err
	}
	if _, err := r.readBytes(int(csLen)); err != nil {
		return "", err
	}
	// compression_methods length (1).
	cmLen, err := r.readByte()
	if err != nil {
		return "", err
	}
	if _, err := r.readBytes(int(cmLen)); err != nil {
		return "", err
	}
	// Extensions length (2). Optional — TLS 1.0 ClientHello can skip it.
	if r.remaining() < 2 {
		return "", ErrNoSNI
	}
	extTotalLen, err := r.readUint16()
	if err != nil {
		return "", err
	}
	if int(extTotalLen) > r.remaining() {
		return "", fmt.Errorf("extensions truncated")
	}

	end := r.pos + int(extTotalLen)
	for r.pos < end {
		extType, err := r.readUint16()
		if err != nil {
			return "", err
		}
		extLen, err := r.readUint16()
		if err != nil {
			return "", err
		}
		extData, err := r.readBytes(int(extLen))
		if err != nil {
			return "", err
		}
		// server_name extension is type 0.
		if extType != 0 {
			continue
		}
		return parseServerName(extData)
	}
	return "", ErrNoSNI
}

// parseServerName parses the body of a server_name extension and returns
// the first host_name (type 0) entry.
func parseServerName(data []byte) (string, error) {
	r := newReader(data)
	// server_name_list length (2).
	listLen, err := r.readUint16()
	if err != nil {
		return "", err
	}
	if int(listLen) > r.remaining() {
		return "", fmt.Errorf("server_name list truncated")
	}
	end := r.pos + int(listLen)
	for r.pos < end {
		nameType, err := r.readByte()
		if err != nil {
			return "", err
		}
		nameLen, err := r.readUint16()
		if err != nil {
			return "", err
		}
		nameBytes, err := r.readBytes(int(nameLen))
		if err != nil {
			return "", err
		}
		if nameType == 0 { // host_name
			return strings.ToLower(string(nameBytes)), nil
		}
	}
	return "", ErrNoSNI
}

// reader is a tiny byte-slice cursor used by parseSNI/parseServerName.
type reader struct {
	buf []byte
	pos int
}

func newReader(buf []byte) *reader { return &reader{buf: buf} }

func (r *reader) remaining() int { return len(r.buf) - r.pos }

func (r *reader) readByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) readUint16() (uint16, error) {
	if r.pos+2 > len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.BigEndian.Uint16(r.buf[r.pos : r.pos+2])
	r.pos += 2
	return v, nil
}

func (r *reader) readBytes(n int) ([]byte, error) {
	if r.pos+n > len(r.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}
