package utils

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// writeOlricConfig writes an Olric config and the unit env file that names it,
// and returns the env dir and the config's directory (the rootfs anchor).
func writeOlricConfig(t *testing.T, namespace, config string) (envDir, anchor string) {
	t.Helper()
	envDir, anchor = t.TempDir(), t.TempDir()
	cfg := filepath.Join(anchor, "olric.yaml")
	if err := os.WriteFile(cfg, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(envDir, namespace), 0o755); err != nil {
		t.Fatal(err)
	}
	env := "NODE_ID=x\nOLRIC_SERVER_CONFIG=" + cfg + "\n"
	if err := os.WriteFile(filepath.Join(envDir, namespace, "olric.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return envDir, anchor
}

// nonLoopbackIPv4 is an address of this host that localhost does not reach.
func nonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("no interface addresses: %v", err)
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		return ipnet.IP.String()
	}
	t.Skip("this host has no non-loopback IPv4 address")
	return ""
}

// Both shapes the node writes: the index config quotes the address, the
// namespace spawner's does not (read from athena's stagenet configs).
func TestGetOlricMemberlistAddr_usesBindAddr(t *testing.T) {
	for name, config := range map[string]string{
		"index":     "server:\n  bindAddr: \"10.0.0.1\"\n  bindPort: 10102\nmemberlist:\n  environment: lan\n  bindAddr: \"10.0.0.1\"\n  bindPort: 10103\n  advertiseAddr: \"10.0.0.1\"\n",
		"namespace": "memberlist:\n    environment: lan\n    bindAddr: 10.0.0.1\n    bindPort: 10008\n    peers:\n        - 10.0.0.2:10008\n",
	} {
		t.Run(name, func(t *testing.T) {
			envDir, anchor := writeOlricConfig(t, "ns", config)
			got, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "ns")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got, "10.0.0.1:") {
				t.Fatalf("addr = %q, want the WireGuard bind address, not localhost", got)
			}
		})
	}
}

// An unset or wildcard bindAddr listens on every interface, loopback included.
func TestGetOlricMemberlistAddr_wildcardDialsLoopback(t *testing.T) {
	for name, config := range map[string]string{
		"unset":    "memberlist:\n  bindPort: 10203\n",
		"0.0.0.0":  "memberlist:\n  bindAddr: 0.0.0.0\n  bindPort: 10203\n",
		"ipv6-any": "memberlist:\n  bindAddr: \"::\"\n  bindPort: 10203\n",
	} {
		t.Run(name, func(t *testing.T) {
			envDir, anchor := writeOlricConfig(t, "anchat", config)
			got, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "anchat")
			if err != nil {
				t.Fatal(err)
			}
			if got != "127.0.0.1:10203" {
				t.Fatalf("addr = %q, want 127.0.0.1:10203", got)
			}
		})
	}
}

func TestGetOlricMemberlistAddr_errors(t *testing.T) {
	t.Run("no env file", func(t *testing.T) {
		envDir, anchor := writeOlricConfig(t, "anchat", "memberlist:\n  bindPort: 10203\n")
		if _, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "absent"); err == nil {
			t.Fatal("a namespace with no env file has no address to wait for")
		}
	})
	t.Run("no memberlist port", func(t *testing.T) {
		envDir, anchor := writeOlricConfig(t, "anchat", "server:\n  bindPort: 10202\n")
		_, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "anchat")
		if err == nil || !strings.Contains(err.Error(), "bindPort") {
			t.Fatalf("err = %v, want a missing bindPort error", err)
		}
	})
	t.Run("env without config path", func(t *testing.T) {
		envDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(envDir, "anchat"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(envDir, "anchat", "olric.env"), []byte("NODE_ID=x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := getOlricMemberlistAddr(envDir, rootfs.At(t.TempDir()), "anchat")
		if err == nil || !strings.Contains(err.Error(), "OLRIC_SERVER_CONFIG") {
			t.Fatalf("err = %v, want a missing OLRIC_SERVER_CONFIG error", err)
		}
	})
}

// The Olric config lives in the orama user's tree and this runs as root: a
// symlink planted in its place is not followed.
func TestGetOlricMemberlistAddr_symlinkedConfigRefused(t *testing.T) {
	envDir, anchor := t.TempDir(), t.TempDir()
	target := filepath.Join(t.TempDir(), "olric.yaml")
	if err := os.WriteFile(target, []byte("memberlist:\n  bindPort: 10203\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(anchor, "olric.yaml")
	if err := os.Symlink(target, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(envDir, "anchat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "anchat", "olric.env"), []byte("OLRIC_SERVER_CONFIG="+cfg+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "anchat"); err == nil {
		t.Errorf("addr = %q read through a symlink, want an error", got)
	}
}

// The stagenet bug: Olric listens on the node's WireGuard address only, and
// the wait dialled localhost, so it never connected and burned 30s per
// namespace per node. A listener bound to a non-loopback address must be
// found through the address the config names.
func TestWaitForOlric_listenerOnNonLoopbackAddress(t *testing.T) {
	ip := nonLoopbackIPv4(t)
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", ip, err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// This host's address stands in for the WireGuard one a node binds.
	old := olricBindNet
	olricBindNet = func(a netip.Addr) bool { return a.String() == ip }
	t.Cleanup(func() { olricBindNet = old })

	config := "memberlist:\n  bindAddr: " + ip + "\n  bindPort: " + strconv.Itoa(port) + "\n"
	envDir, anchor := writeOlricConfig(t, "index", config)
	addr, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "index")
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForTCPAddr(addr, 3*time.Second); err != nil {
		t.Fatalf("wait on %s: %v", addr, err)
	}

	// The old dial target does not reach this listener.
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second); err == nil {
		conn.Close()
		t.Skip("loopback reaches the listener on this host; the regression cannot be shown here")
	}
}

func TestWaitForTCPAddr_timeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens

	start := time.Now()
	err = waitForTCPAddr(addr, 2*time.Second)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), addr) {
		t.Fatalf("error %q does not name %s", err, addr)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the wait overran its budget: %s", elapsed)
	}
}

func TestWaitForTCPAddr_delayedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ready := make(chan net.Listener, 1)
	go func() {
		time.Sleep(1500 * time.Millisecond)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			ready <- nil
			return
		}
		ready <- l
	}()
	defer func() {
		if l := <-ready; l != nil {
			l.Close()
		}
	}()

	if err := waitForTCPAddr(addr, 10*time.Second); err != nil {
		t.Fatalf("expected success once the listener came up: %v", err)
	}
}

// The config sits where its namespace's gateway can write: a name, or an
// address off the node's own networks, is refused rather than dialled.
func TestGetOlricMemberlistAddr_refusesNamesAndForeignAddresses(t *testing.T) {
	for _, bind := range []string{"evil.example.com", "8.8.8.8", "\x1b[2Jhost"} {
		envDir, anchor := writeOlricConfig(t, "index", "memberlist:\n  bindAddr: \""+bind+"\"\n  bindPort: 10103\n")
		if got, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "index"); err == nil {
			t.Errorf("bindAddr %q accepted as %q", bind, got)
		}
	}
	envDir, anchor := writeOlricConfig(t, "index", "memberlist:\n  bindAddr: 10.0.0.2\n  bindPort: 10103\n")
	if got, err := getOlricMemberlistAddr(envDir, rootfs.At(anchor), "index"); err != nil || got != "10.0.0.2:10103" {
		t.Fatalf("WireGuard bindAddr = %q, %v", got, err)
	}
}

// A node an older CLI stopped has the ipfs-gc service itself masked; start
// and upgrade unmask what a timer runs as well as the timer.
func TestTimerBackingServices_coversGCService(t *testing.T) {
	units := []string{"orama-node", "orama-namespace-ipfs-gc@index.timer", "orama-namespace-gateway@index"}
	got := TimerBackingServices(units)
	if len(got) != 1 || got[0] != "orama-namespace-ipfs-gc@index.service" {
		t.Fatalf("backing services = %v, want the ipfs-gc oneshot", got)
	}
	if got := TimerBackingServices(nil); len(got) != 0 {
		t.Fatalf("no units gave %v", got)
	}
}
