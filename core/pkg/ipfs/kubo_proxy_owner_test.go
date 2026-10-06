package ipfs

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"runtime"
	"testing"
)

var (
	proxyAddr  = netip.MustParseAddrPort("127.0.0.1:10110")
	clientAddr = netip.MustParseAddrPort("127.0.0.1:55712")
)

func TestSockDiagRequest_namesTheExactSocket(t *testing.T) {
	b, err := sockDiagRequest(clientAddr, proxyAddr, 7)
	if err != nil {
		t.Fatal(err)
	}
	ne := binary.NativeEndian
	if len(b) != nlmsgHeaderLen+inetDiagReqV2Len || ne.Uint32(b) != uint32(len(b)) {
		t.Fatalf("message length %d, header says %d", len(b), ne.Uint32(b))
	}
	if ne.Uint16(b[4:]) != sockDiagByFamily || ne.Uint16(b[6:]) != nlmFRequest || ne.Uint32(b[8:]) != 7 {
		t.Fatalf("header = % x", b[:nlmsgHeaderLen])
	}
	req := b[nlmsgHeaderLen:]
	if req[0] != afInet || req[1] != ipprotoTCP {
		t.Fatalf("family/protocol = %d/%d", req[0], req[1])
	}
	if sport, dport := binary.BigEndian.Uint16(req[sockIDSportOff:]), binary.BigEndian.Uint16(req[sockIDSportOff+2:]); sport != 55712 || dport != 10110 {
		t.Fatalf("ports = %d->%d, want the dialling socket 55712->10110", sport, dport)
	}
	if src := netip.AddrFrom4([4]byte(req[sockIDSrcOff : sockIDSrcOff+4])); src != clientAddr.Addr() {
		t.Fatalf("source = %s", src)
	}
	if ne.Uint32(req[sockIDCookieOff:]) != inetDiagNoCookie {
		t.Fatal("the cookie must be INET_DIAG_NOCOOKIE for a lookup by address")
	}
}

func TestSockDiagRequest_refusesIPv6(t *testing.T) {
	if _, err := sockDiagRequest(netip.MustParseAddrPort("[::1]:5000"), proxyAddr, 1); err == nil {
		t.Fatal("an IPv6 socket was encoded into an IPv4 query")
	}
}

func diagReply(typ uint16, body []byte) []byte {
	b := make([]byte, nlmsgHeaderLen+len(body))
	binary.NativeEndian.PutUint32(b, uint32(len(b)))
	binary.NativeEndian.PutUint16(b[4:], typ)
	copy(b[nlmsgHeaderLen:], body)
	return b
}

func TestParseSockDiagReply_readsTheUID(t *testing.T) {
	msg := make([]byte, inetDiagMsgMinLen)
	binary.NativeEndian.PutUint32(msg[inetDiagMsgUIDOff:], 61234)
	uid, err := parseSockDiagReply(diagReply(sockDiagByFamily, msg))
	if err != nil || uid != 61234 {
		t.Fatalf("uid = %d, %v", uid, err)
	}
}

func TestParseSockDiagReply_errors(t *testing.T) {
	enoentBody := make([]byte, nlmsgErrorCodeSize)
	notFound, denied := int32(-enoent), int32(-1)
	binary.NativeEndian.PutUint32(enoentBody, uint32(notFound))
	if _, err := parseSockDiagReply(diagReply(nlmsgError, enoentBody)); !errors.Is(err, errSocketNotFound) {
		t.Fatalf("ENOENT = %v, want errSocketNotFound", err)
	}
	permBody := make([]byte, nlmsgErrorCodeSize)
	binary.NativeEndian.PutUint32(permBody, uint32(denied))
	for name, reply := range map[string][]byte{
		"empty":           nil,
		"other errno":     diagReply(nlmsgError, permBody),
		"truncated error": diagReply(nlmsgError, nil),
		"short msg":       diagReply(sockDiagByFamily, make([]byte, inetDiagMsgMinLen-1)),
		"unknown type":    diagReply(99, make([]byte, inetDiagMsgMinLen)),
	} {
		if _, err := parseSockDiagReply(reply); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

// On Linux the kernel names this process as the owner of a socket it
// dialled — the lookup ServeCluster makes for every connection.
func TestSockDiagOwner_live(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sock_diag is Linux's")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if s := <-accepted; s != nil {
		defer s.Close()
	}
	uid, err := sockDiagOwner(netip.MustParseAddrPort(c.LocalAddr().String()), netip.MustParseAddrPort(c.RemoteAddr().String()))
	if err != nil {
		t.Fatal(err)
	}
	if uid != uint32(os.Getuid()) {
		t.Fatalf("owner = %d, want %d", uid, os.Getuid())
	}
}

func TestSockDiagOwner_offLinuxRefuses(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("the refusal is the non-Linux build's")
	}
	if _, err := sockDiagOwner(clientAddr, proxyAddr); err == nil {
		t.Fatal("an owner was reported without a kernel to ask")
	}
}
